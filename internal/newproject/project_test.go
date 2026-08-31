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

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/projectcheck"
	"github.com/plystra/cli/internal/projectsmoke"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

var updateProjectGolden = flag.Bool("update", false, "update generated project scaffold golden files")

const (
	templateDriftHelperEnvironment = "PLYSTRA_TEMPLATE_DRIFT_HELPER"
	templateDriftGoEnvironment     = "PLYSTRA_TEMPLATE_DRIFT_REAL_GO"
	templateDriftCountEnvironment  = "PLYSTRA_TEMPLATE_DRIFT_COUNT"
	templateDriftFailEnvironment   = "PLYSTRA_TEMPLATE_DRIFT_FAIL"
	templateCheckFailEnvironment   = "PLYSTRA_TEMPLATE_CHECK_FAIL"
	templateBuildFailEnvironment   = "PLYSTRA_TEMPLATE_BUILD_FAIL"
	newProjectQuerySemanticsYAML   = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
)

func TestKernelVersionMatchesCLIModuleRequirement(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("Parse(go.mod): %v", err)
	}
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path != "github.com/plystra/kernel" {
			continue
		}
		if requirement.Indirect {
			t.Fatal("CLI Kernel requirement is indirect")
		}
		if requirement.Mod.Version != newproject.KernelVersion {
			t.Fatalf("new Project Kernel version %s does not match CLI requirement %s", newproject.KernelVersion, requirement.Mod.Version)
		}
		return
	}
	t.Fatal("CLI go.mod has no direct github.com/plystra/kernel requirement")
}

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_NPM_HELPER") == "1" {
		os.Exit(runNPMHelper())
	}
	if os.Getenv(templateDriftHelperEnvironment) == "1" {
		os.Exit(runTemplateDriftGoHelper())
	}
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

func runNPMHelper() int {
	if len(os.Args) < 2 {
		_, _ = fmt.Fprintln(os.Stderr, "missing npm arguments")
		return 2
	}
	arguments := strings.Join(os.Args[1:], " ")
	if logPath := os.Getenv("PLYSTRA_NPM_LOG"); logPath != "" {
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "open npm log: %v", err)
			return 2
		}
		_, _ = fmt.Fprintln(file, arguments)
		_ = file.Close()
	}
	if os.Getenv("PLYSTRA_NPM_FAIL_ON") == arguments {
		_, _ = fmt.Fprintf(os.Stderr, "injected npm failure for %s\n", arguments)
		return 17
	}
	root, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "get npm working directory: %v\n", err)
		return 2
	}
	switch {
	case os.Args[1] == "install":
		if err := os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0o755); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "create node_modules: %v\n", err)
			return 2
		}
	case os.Args[1] == "run" && len(os.Args) >= 3 && os.Args[2] == "build":
		if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "create dist: %v\n", err)
			return 2
		}
		if err := os.WriteFile(filepath.Join(root, "dist", "index.js"), []byte("compiled\n"), 0o644); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write dist: %v\n", err)
			return 2
		}
	}
	return 0
}

func runTemplateDriftGoHelper() int {
	if len(os.Args) == 6 && reflect.DeepEqual(os.Args[1:], []string{"list", "-m", "-json", "-mod=readonly", "all"}) {
		countFile := os.Getenv(templateDriftCountEnvironment)
		count := 0
		if data, err := os.ReadFile(countFile); err == nil {
			if _, err := fmt.Sscanf(string(data), "%d", &count); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "parse template drift count: %v\n", err)
				return 125
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(os.Stderr, "read template drift count: %v\n", err)
			return 125
		}
		count++
		if err := os.WriteFile(countFile, fmt.Appendf(nil, "%d", count), 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write template drift count: %v\n", err)
			return 125
		}
		if count == 4 && os.Getenv(templateCheckFailEnvironment) != "1" && os.Getenv(templateBuildFailEnvironment) != "1" {
			if os.Getenv(templateDriftFailEnvironment) == "1" {
				_, _ = fmt.Fprintln(os.Stderr, "injected generated stability check failure")
				return 124
			}
			root, err := os.Getwd()
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "locate staged template: %v\n", err)
				return 125
			}
			path := filepath.Join(root, "generated", "go", "assembly", "compatibility_gen.go")
			data, err := os.ReadFile(path)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "read generated template file: %v\n", err)
				return 125
			}
			data = append(data, []byte("\n// injected drift during qualified-template checking\n")...)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "change generated template file: %v\n", err)
				return 125
			}
		}
	}
	if os.Getenv(templateCheckFailEnvironment) == "1" && len(os.Args) == 4 && reflect.DeepEqual(os.Args[1:], []string{"test", "-mod=readonly", "./..."}) {
		data, err := os.ReadFile(os.Getenv(templateDriftCountEnvironment))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "read template check count: %v\n", err)
			return 125
		}
		if string(data) == "5" {
			_, _ = fmt.Fprintln(os.Stderr, "injected qualified-template plystra check failure")
			return 123
		}
	}
	if os.Getenv(templateBuildFailEnvironment) == "1" && len(os.Args) == 4 && reflect.DeepEqual(os.Args[1:], []string{"build", "-mod=readonly", "./..."}) {
		data, err := os.ReadFile(os.Getenv(templateDriftCountEnvironment))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "read template build count: %v\n", err)
			return 125
		}
		if string(data) == "5" {
			_, _ = fmt.Fprintln(os.Stderr, "injected qualified-template build failure")
			return 122
		}
	}

	command := exec.Command(os.Getenv(templateDriftGoEnvironment), os.Args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode()
		}
		_, _ = fmt.Fprintf(os.Stderr, "run real Go command: %v\n", err)
		return 125
	}
	return 0
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
		"generated/compatibility/interface-documentation.json",
		"generated/compatibility/interface-javascript.json",
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
	for _, required := range [][]byte{[]byte("interfaces:"), []byte("  require: []"), []byte("  use: {}"), []byte("  policies: {}")} {
		if !bytes.Contains(directTree["plystra.yaml"], required) {
			t.Fatalf("project scaffold omits %q:\n%s", required, directTree["plystra.yaml"])
		}
	}
	for _, obsolete := range [][]byte{[]byte("capabilities:"), []byte("aliases:")} {
		if bytes.Contains(directTree["plystra.yaml"], obsolete) {
			t.Fatalf("project scaffold retains obsolete declaration %q:\n%s", obsolete, directTree["plystra.yaml"])
		}
	}
	assertDefaultTransportScaffold(t, directTree["plystra.yaml"])
	assertReadmeUsesAvailableCommands(t, directTree["README.md"])
	assertCIUsesCurrentActions(t, directTree[".github/workflows/ci.yml"])
	assertPlystraSkill(t, direct.Path(), modulePath)
	provenance, err := applicationgen.DecodeManifestProvenance(directTree["generated/manifest.json"])
	if err != nil || !provenance.TransportToolchain().Valid() {
		t.Fatalf("generated Project transport toolchain = %#v, %v", provenance.TransportToolchain(), err)
	}
	if !bytes.Contains(directTree["generated/.plystra-manifest.json"], []byte(provenance.TransportToolchain().Digest())) {
		t.Fatalf("generated Project ownership manifest omits toolchain digest %q", provenance.TransportToolchain().Digest())
	}
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
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":      []byte("package platform\n\nconst Identity = \"template-source-only\"\n"),
		"TEMPLATE_ONLY.md": []byte("This file must remain in the dependency source.\n"),
		"plystra.yaml": []byte(`http:
  expose:
    - kernel.health/v1

interfaces:
  require:
    - email.send/v1
    - kernel.info/v1
  use:
    email.send/v1: example.com/acme/platform/mailer.New
`),
		"plystra.production.yaml": []byte("interfaces:\n  require:\n    - missing.overlay/v1\n"),
		"interfaces/email/send/v1/interface.go": []byte(`package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`),
		"mailer/mailer.go": []byte(`package mailer

import (
	"context"

	sendv1 "example.com/acme/platform/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}

var _ sendv1.Interface = (*Service)(nil)
`),
	})
	environment := setEnvironmentValue(isolatedGoEnvironment(t, proxy), "GOPRIVATE", "corp.example.com")
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
	wantOutput := fmt.Sprintf(
		"Created my-app from %s\nConfiguration scaffolded\nGenerated, checked, built, and locally verified\n\nNext:\n  cd my-app\n  plystra check\n",
		templateQuery,
	)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	assertDirectRequirement(t, target, templatePath, templateVersion)
	configuration, err := os.ReadFile(filepath.Join(target, "plystra.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(plystra.yaml): %v", err)
	}
	assertDefaultTransportScaffold(t, configuration)
	model, err := applicationmeta.Parse(configuration)
	if err != nil {
		t.Fatalf("Parse(plystra.yaml): %v", err)
	}
	exposures := model.HTTPExposures()
	if len(exposures) != 0 {
		t.Fatalf("dependency HTTP exposure entered created Project = %#v", exposures)
	}
	if transports := model.HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("composed HTTP transports = %#v, want Connect enabled and REST disabled", transports)
	}
	requirements := model.InterfaceRequirements()
	if len(requirements) != 2 || requirements[0].ID().String() != "email.send/v1" || requirements[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("composed requirements = %#v, want email.send/v1 and kernel.info/v1", requirements)
	}
	choices := model.ImplementationChoices()
	if len(choices) != 1 || choices[0].InterfaceID().String() != "email.send/v1" || choices[0].Constructor().String() != "example.com/acme/platform/mailer.New" {
		t.Fatalf("composed Implementation choices = %#v, want email.send/v1 -> example.com/acme/platform/mailer.New", choices)
	}
	if bytes.Contains(configuration, []byte("missing.overlay/v1")) {
		t.Fatalf("dependency environment overlay was inherited:\n%s", configuration)
	}
	for _, copied := range []string{"template.go", "TEMPLATE_ONLY.md", "plystra.production.yaml", "interfaces", "mailer", "go.work"} {
		if _, err := os.Lstat(filepath.Join(target, copied)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("template source path %s was copied into the Project: %v", copied, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(target, "generated", "manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte(templatePath)) {
		t.Fatalf("generated manifest template provenance = %q, %v", manifest, err)
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

func TestCreateIgnoresTemplateExposureWithoutGeneratingJavaScriptSDK(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/javascript-platform"
	const templateVersion = "v1.0.0"
	templateQuery := templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("http:\n  expose: [kernel.health/v1]\n"),
	})
	logPath := filepath.Join(t.TempDir(), "npm.log")
	environment := isolatedGoEnvironment(t, proxy)
	environment = setEnvironmentValue(environment, "PLYSTRA_NPM_HELPER", "1")
	environment = setEnvironmentValue(environment, "PLYSTRA_NPM_LOG", logPath)
	parent := t.TempDir()
	result, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		NPMCommand:  os.Args[0],
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Path() != filepath.Join(parent, "my-app") {
		t.Fatalf("result path = %q", result.Path())
	}
	assertDirectRequirement(t, result.Path(), templatePath, templateVersion)
	if _, err := os.Lstat(filepath.Join(result.Path(), "go.work")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated Project contains go.work: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(result.Path(), "plystra.yaml"))
	if err != nil {
		t.Fatalf("read created Project configuration: %v", err)
	}
	manifest, err := applicationmeta.Parse(configuration)
	if err != nil || len(manifest.HTTPExposures()) != 0 {
		t.Fatalf("created Project exposure = %#v, %v", manifest.HTTPExposures(), err)
	}
	if _, err := os.Lstat(filepath.Join(result.Path(), "generated", "sdk", "javascript")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored dependency exposure generated a JavaScript SDK: %v", err)
	}
	if err := gocommand.Run(t.Context(), gocommand.Options{
		Directory:   result.Path(),
		Environment: environment,
	}, "list", "-mod=readonly", "./..."); err != nil {
		t.Fatalf("created Project does not resolve with GOWORK=off: %v", err)
	}
	if _, err := os.Lstat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored dependency exposure invoked npm validation: %v", err)
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

func TestPublicCommandRejectsTemplateWithAmbiguousDefaultProvidersAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/ambiguous-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	contract := []byte("id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n" + newProjectQuerySemanticsYAML)
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":        []byte("package platform\n"),
		"plystra.yaml":       []byte("capabilities:\n  require:\n    - email.send/v1\n"),
		"memory/plugin.yaml": []byte("id: acme.platform.memory\nprovides: [email.send/v1]\n"),
		"memory/capabilities/email.send/v1/capability.yaml": contract,
		"smtp/plugin.yaml": []byte("id: acme.platform.smtp\nprovides: [email.send/v1]\n"),
		"smtp/capabilities/email.send/v1/capability.yaml": contract,
	})
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{
		"new", "my-app",
		"--module", "example.com/acme/my-app",
		"--template", templateQuery,
		"--no-git", "--no-github-ci", "--no-skills",
	}

	if exitCode := command.RunIn(arguments, &stdout, &stderr, parent, environment); exitCode != 1 {
		t.Fatalf("RunIn exit code = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("RunIn stdout = %q, want empty output", stdout.String())
	}
	for _, detail := range []string{
		"invalid Plystra Project template",
		templateQuery,
		"cannot qualify because its default Provider model is ambiguous",
		"ambiguous canonical Capability provider",
		"email.send/v1",
		"acme.platform.memory",
		"acme.platform.smtp",
		"template publisher must add the listed capabilities.use choices to its root plystra.yaml",
	} {
		if !strings.Contains(stderr.String(), detail) {
			t.Fatalf("RunIn stderr omits %q: %s", detail, stderr.String())
		}
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after ambiguous template failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestPublicCommandRejectsPrivateTemplateGraphAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/private-dependent-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	const privateRuntimePath = "example.com/acme/private-runtime"
	const privateRuntimeVersion = "v1.2.0"
	const privateToolsPath = "example.com/acme/private-tools"
	const privateToolsVersion = "v1.1.0"
	writeProxyModule(t, proxy, privateRuntimePath, privateRuntimeVersion, map[string][]byte{
		"runtime.go": []byte("package runtime\n"),
	})
	writeProxyModule(t, proxy, privateToolsPath, privateToolsVersion, map[string][]byte{
		"tools.go": []byte("package tools\n"),
	})
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"go.mod":       []byte("module " + templatePath + "\n\ngo 1.26\n\nrequire (\n\t" + privateRuntimePath + " " + privateRuntimeVersion + "\n\t" + privateToolsPath + " " + privateToolsVersion + "\n)\n"),
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	environment := setEnvironmentValue(
		isolatedGoEnvironment(t, proxy),
		"GOPRIVATE",
		templatePath+","+privateRuntimePath+","+privateToolsPath,
	)
	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{
		"new", "my-app",
		"--module", "example.com/acme/my-app",
		"--template", templateQuery,
		"--no-git", "--no-github-ci", "--no-skills",
	}

	if exitCode := command.RunIn(arguments, &stdout, &stderr, parent, environment); exitCode != 1 {
		t.Fatalf("RunIn exit code = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("RunIn stdout = %q, want empty output", stdout.String())
	}
	for _, detail := range []string{
		"invalid Plystra Project template",
		templateQuery,
		"cannot qualify because its effective Go Module graph requires private modules matched by GOPRIVATE",
		privateRuntimePath + "@" + privateRuntimeVersion,
		privateToolsPath + "@" + privateToolsVersion,
		"qualified templates must use only public modules",
		"correct an overbroad GOPRIVATE setting",
	} {
		if !strings.Contains(stderr.String(), detail) {
			t.Fatalf("RunIn stderr omits %q: %s", detail, stderr.String())
		}
	}
	_, privateList, found := strings.Cut(strings.SplitN(stderr.String(), "\n\nRecovery:", 2)[0], "matched by GOPRIVATE: ")
	if !found {
		t.Fatalf("RunIn stderr omits private module list: %s", stderr.String())
	}
	wantPrivateList := templateQuery + ", " + privateRuntimePath + "@" + privateRuntimeVersion + ", " + privateToolsPath + "@" + privateToolsVersion
	if privateList != wantPrivateList {
		t.Fatalf("RunIn private module list = %q, want %q", privateList, wantPrivateList)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after private dependency failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestPublicCommandRejectsRelativeReplacementsAcrossTemplateProjectsAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const supportPath = "example.com/acme/support"
	const supportVersion = "v1.3.0"
	const basePath = "example.com/acme/relative-base"
	const baseVersion = "v1.1.0"
	const templatePath = "example.com/acme/relative-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, supportPath, supportVersion, map[string][]byte{
		"support.go": []byte("package support\n"),
	})
	writeProxyModule(t, proxy, basePath, baseVersion, map[string][]byte{
		"go.mod":       []byte("module " + basePath + "\n\ngo 1.26\n\nrequire " + supportPath + " " + supportVersion + "\n\nreplace " + supportPath + " " + supportVersion + " => ../support\n"),
		"base.go":      []byte("package base\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"go.mod":       []byte("module " + templatePath + "\n\ngo 1.26\n\nrequire " + basePath + " " + baseVersion + "\n\nreplace " + basePath + " " + baseVersion + " => ../relative-base\n"),
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{
		"new", "my-app",
		"--module", "example.com/acme/my-app",
		"--template", templateQuery,
		"--no-git", "--no-github-ci", "--no-skills",
	}

	if exitCode := command.RunIn(arguments, &stdout, &stderr, parent, environment); exitCode != 1 {
		t.Fatalf("RunIn exit code = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("RunIn stdout = %q, want empty output", stdout.String())
	}
	for _, detail := range []string{
		"invalid Plystra Project template",
		templateQuery,
		"cannot qualify because dependency Plystra Projects declare relative Go Module replacements",
		basePath + "@" + baseVersion + "/go.mod: replace " + supportPath + "@" + supportVersion + " => ../support",
		templatePath + "@" + templateVersion + "/go.mod: replace " + basePath + "@" + baseVersion + " => ../relative-base",
		"publish every required module version and remove each relative replace",
	} {
		if !strings.Contains(stderr.String(), detail) {
			t.Fatalf("RunIn stderr omits %q: %s", detail, stderr.String())
		}
	}
	_, replacementList, found := strings.Cut(strings.SplitN(stderr.String(), "\n\nRecovery:", 2)[0], "replacements: ")
	if !found {
		t.Fatalf("RunIn stderr omits relative replacement list: %s", stderr.String())
	}
	wantReplacementList := basePath + "@" + baseVersion + "/go.mod: replace " + supportPath + "@" + supportVersion + " => ../support; " + templatePath + "@" + templateVersion + "/go.mod: replace " + basePath + "@" + baseVersion + " => ../relative-base"
	if replacementList != wantReplacementList {
		t.Fatalf("RunIn relative replacement list = %q, want %q", replacementList, wantReplacementList)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after relative replace failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRejectsImmediateGeneratedDriftAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/unstable-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("LookPath(go): %v", err)
	}
	countFile := filepath.Join(t.TempDir(), "module-discovery-count")
	environment := isolatedGoEnvironment(t, proxy)
	environment = setEnvironmentValue(environment, templateDriftHelperEnvironment, "1")
	environment = setEnvironmentValue(environment, templateDriftGoEnvironment, realGo)
	environment = setEnvironmentValue(environment, templateDriftCountEnvironment, countFile)
	parent := t.TempDir()

	_, err = newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		GoCommand:   os.Args[0],
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) {
		t.Fatalf("Create error = %v", err)
	}
	for _, detail := range []string{
		templateQuery,
		"generated output is not stable immediately after installation",
		"manually-modified generated/go/assembly/compatibility_gen.go",
		"template publisher must make generation deterministic",
		"plystra generate --check",
		"publish a corrected module version",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Create error omits %q: %v", detail, err)
		}
	}
	if data, readErr := os.ReadFile(countFile); readErr != nil || string(data) != "4" {
		t.Fatalf("module discovery count = %q, %v; immediate check did not run exactly once", data, readErr)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after generated drift failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateClassifiesImmediateGeneratedCheckFailureAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/uncheckable-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("LookPath(go): %v", err)
	}
	countFile := filepath.Join(t.TempDir(), "module-discovery-count")
	environment := isolatedGoEnvironment(t, proxy)
	environment = setEnvironmentValue(environment, templateDriftHelperEnvironment, "1")
	environment = setEnvironmentValue(environment, templateDriftGoEnvironment, realGo)
	environment = setEnvironmentValue(environment, templateDriftCountEnvironment, countFile)
	environment = setEnvironmentValue(environment, templateDriftFailEnvironment, "1")
	parent := t.TempDir()

	_, err = newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		GoCommand:   os.Args[0],
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) || !errors.Is(err, applicationgenerate.ErrGenerate) {
		t.Fatalf("Create error = %v", err)
	}
	for _, detail := range []string{
		templateQuery,
		"generated stability checking failed immediately after installation",
		"injected generated stability check failure",
		"template publisher must make generation deterministic",
		"plystra generate --check",
		"publish a corrected module version",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Create error omits %q: %v", detail, err)
		}
	}
	if data, readErr := os.ReadFile(countFile); readErr != nil || string(data) != "4" {
		t.Fatalf("module discovery count = %q, %v; immediate check did not run exactly once", data, readErr)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after generated stability check failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRunsPlystraCheckAndRollsBackItsFailure(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/check-failing-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("LookPath(go): %v", err)
	}
	countFile := filepath.Join(t.TempDir(), "module-discovery-count")
	environment := isolatedGoEnvironment(t, proxy)
	environment = setEnvironmentValue(environment, templateDriftHelperEnvironment, "1")
	environment = setEnvironmentValue(environment, templateDriftGoEnvironment, realGo)
	environment = setEnvironmentValue(environment, templateDriftCountEnvironment, countFile)
	environment = setEnvironmentValue(environment, templateCheckFailEnvironment, "1")
	parent := t.TempDir()

	_, err = newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		GoCommand:   os.Args[0],
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) || !errors.Is(err, projectcheck.ErrCheck) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v", err)
	}
	for _, detail := range []string{
		templateQuery,
		"plystra check failed during creation",
		"injected qualified-template plystra check failure",
		"template publisher must run plystra check successfully",
		"publish a corrected module version",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Create error omits %q: %v", detail, err)
		}
	}
	if data, readErr := os.ReadFile(countFile); readErr != nil || string(data) != "5" {
		t.Fatalf("module discovery count = %q, %v; qualified-template check did not run at the expected boundary", data, readErr)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after qualified-template check failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateBuildsTemplateProjectAndRollsBackFailure(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/build-failing-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("LookPath(go): %v", err)
	}
	countFile := filepath.Join(t.TempDir(), "module-discovery-count")
	environment := isolatedGoEnvironment(t, proxy)
	environment = setEnvironmentValue(environment, templateDriftHelperEnvironment, "1")
	environment = setEnvironmentValue(environment, templateDriftGoEnvironment, realGo)
	environment = setEnvironmentValue(environment, templateDriftCountEnvironment, countFile)
	environment = setEnvironmentValue(environment, templateBuildFailEnvironment, "1")
	parent := t.TempDir()

	_, err = newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		GoCommand:   os.Args[0],
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v", err)
	}
	for _, detail := range []string{
		templateQuery,
		"staged Project build failed",
		"injected qualified-template build failure",
		"go build -mod=readonly ./...",
		"publish a corrected module version",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Create error omits %q: %v", detail, err)
		}
	}
	if data, readErr := os.ReadFile(countFile); readErr != nil || string(data) != "5" {
		t.Fatalf("module discovery count = %q, %v; qualified-template build did not run at the expected boundary", data, readErr)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after qualified-template build failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRunsTemplateLifecycleSmokeAndRollsBackFailure(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/unhealthy-platform"
	const templateVersion = "v1.0.0"
	const templateQuery = templatePath + "@" + templateVersion
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":  []byte("package platform\n"),
		"plystra.yaml": []byte("{}\n"),
	})
	environment := setEnvironmentValue(isolatedGoEnvironment(t, proxy), "PLYSTRA_TEST_HEALTH_UNHEALTHY", "1")
	parent := t.TempDir()

	_, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templateQuery,
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) || !errors.Is(err, projectsmoke.ErrSmoke) {
		t.Fatalf("Create error = %v", err)
	}
	for _, detail := range []string{
		templateQuery,
		"staged Project lifecycle smoke failed",
		"kernel.health/v1",
		"stop cleanly",
		"without go.work",
		"publish a corrected module version",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Create error omits %q: %v", detail, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after qualified-template smoke failure: %v", err)
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
	for _, available := range [][]byte{[]byte("plystra add github.com/acme/platform@v1.0.0"), []byte("plystra plugin create"), []byte("plystra capability create"), []byte("plystra generate --check"), []byte("plystra generate --env"), []byte("PLYSTRA_ENV"), []byte("plystra generate --config"), []byte("PLYSTRA_CONFIG"), []byte("plystra inspect"), []byte("plystra check"), []byte("go run ./generated/go/application --env production"), []byte("go run ./generated/go/application --config deploy/customer-a.yaml"), []byte("go test ./..."), []byte("go build ./..."), []byte("go vet ./...")} {
		if !bytes.Contains(readme, available) {
			t.Fatalf("generated README omits available workflow %q:\n%s", available, readme)
		}
	}
	for _, transport := range [][]byte{[]byte("http.transports.connect: true"), []byte("http.transports.rest: false")} {
		if !bytes.Contains(readme, transport) {
			t.Fatalf("generated README omits explicit default transport %q:\n%s", transport, readme)
		}
	}
	if !bytes.Contains(readme, []byte("JavaScript SDK generation requires Connect")) {
		t.Fatalf("generated README omits the JavaScript Connect requirement:\n%s", readme)
	}
	for _, toolchainGuidance := range [][]byte{
		[]byte("top-level `transport_toolchain` record"),
		[]byte("exact embedded `go/format` runtime"),
		[]byte("API-documentation generator versions"),
		[]byte("pinned generated Go and npm dependency versions"),
		[]byte("implicit global `protoc`"),
		[]byte("hosted generation service"),
		[]byte("`plystra generate --check` reports drift"),
	} {
		if !bytes.Contains(readme, toolchainGuidance) {
			t.Fatalf("generated README omits transport-toolchain guidance %q:\n%s", toolchainGuidance, readme)
		}
	}
	for _, compatibilityGuidance := range [][]byte{
		[]byte("`generated/compatibility/interfaces.json`"),
		[]byte("`generated/compatibility/interface-documentation.json`"),
		[]byte("`generated/compatibility/interface-javascript.json`"),
		[]byte("`generated/compatibility/interface-metadata.json`"),
		[]byte("`generated/compatibility/interface-transport.json`"),
		[]byte("every visible authored Interface"),
		[]byte("whether or not it is selected or exposed"),
		[]byte("stable field numbers, Go and JSON names, requiredness, and canonical Go types"),
		[]byte("excluding metadata, projections, Implementations, configuration, Secrets, source paths, and module versions"),
		[]byte("stores only exact-contract, documentation, and example digests, never metadata values"),
		[]byte("contract class covers Go shape, semantics, semantic-error codes, constraints, and Behavioral Conformance declarations"),
		[]byte("documentation covers descriptions and deprecation"),
		[]byte("examples cover validated requests and outcomes"),
		[]byte("selected Connect surface"),
		[]byte("separate Protobuf-descriptor, Connect-procedure, and active wire-map digests"),
		[]byte("shared safe-error descriptor"),
		[]byte("current `generated/docs/api.md` and `generated/docs/openapi.json` artifacts"),
		[]byte("closed artifact kind, stable managed path, and exact content digest"),
		[]byte("valid empty record when no documentation surface is selected"),
		[]byte("`plystra generate` refreshes it transactionally"),
		[]byte("`plystra generate --check` reports drift without mutation"),
		[]byte("`plystra generate --check` compares the classes without mutation"),
		[]byte("Never edit it manually"),
	} {
		if !bytes.Contains(readme, compatibilityGuidance) {
			t.Fatalf("generated README omits Interface compatibility guidance %q:\n%s", compatibilityGuidance, readme)
		}
	}
	for _, credentialGuidance := range [][]byte{
		[]byte("requires one explicit `credentialPolicy`"),
		[]byte(`{mode: "anonymous"}`),
		[]byte(`{mode: "cookie", fetchCredentials: "same-origin"}`),
		[]byte(`{mode: "bearer", getAccessToken}`),
		[]byte("Fetch credentials `omit`"),
		[]byte("code `credential_error`"),
		[]byte("while bearer acquisition is pending"),
		[]byte("never falls back to another"),
	} {
		if !bytes.Contains(readme, credentialGuidance) {
			t.Fatalf("generated README omits JavaScript credential guidance %q:\n%s", credentialGuidance, readme)
		}
	}
	for _, wireHistory := range [][]byte{
		[]byte("generated/proto/wire-map.json"),
		[]byte("messages of every visible authored Interface"),
		[]byte("whether or not the Interface is exposed or Connect is enabled"),
		[]byte("exact stable service, method, and procedure identity"),
		[]byte("Authored positive `plystra` field numbers are the wire numbers"),
		[]byte("Generation rejects renumbering and reuse across all visible Interface history"),
		[]byte("permanently reserves every removed Protobuf field name and number"),
		[]byte("Never-exposed Interfaces, removed exposure, and disabled Connect remain inactive"),
		[]byte("create no schema, descriptor, handler, or SDK output"),
		[]byte("separately labelled legacy transport history"),
		[]byte("not Interface contract authority"),
		[]byte("exactly one unary service from every exposed Interface package"),
		[]byte("Connect procedure path is derived from the exact Interface ID"),
		[]byte("legacy schema is import-only and owns no competing service"),
		[]byte("generated/proto/descriptor-set.pb"),
		[]byte("self-contained"),
		[]byte("contain no Implementation, configuration, or Secret data"),
		[]byte("checked by `plystra generate --check`"),
		[]byte("Binary Protobuf requests are limited to 1 MiB"),
		[]byte("maximum message depth of 64"),
		[]byte("65,536-node budget"),
		[]byte("unknown fields at any message depth"),
		[]byte("Binary Protobuf responses use the same"),
		[]byte("serializes deterministically"),
		[]byte("no partial response"),
		[]byte("ProtoJSON requests independently accept at most 1 MiB"),
		[]byte("65,536 structural tokens"),
		[]byte("Unknown or duplicate fields"),
		[]byte("invalid UTF-8"),
		[]byte("Optional non-nullable null becomes absence"),
		[]byte("full-range integers remain exact"),
		[]byte("ProtoJSON responses use the same exact generated message"),
		[]byte("Canonical and Alias binary and ProtoJSON paths agree"),
	} {
		if !bytes.Contains(readme, wireHistory) {
			t.Fatalf("generated README omits Protobuf wire-history guidance %q:\n%s", wireHistory, readme)
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
		{name: "capability/capability.go", data: []byte("package capability\n\nimport \"context\"\n\ntype Identifier struct { value, name string; major uint32 }\ntype Contract[Request, Response any] struct{ id string }\ntype Handler[Request, Response any] func(context.Context, Request) (Response, error)\n\nfunc ParseIdentifier(id string) (Identifier, error) { return Identifier{value: id, name: id, major: 1}, nil }\nfunc (identifier Identifier) String() string { return identifier.value }\nfunc (identifier Identifier) Name() string { return identifier.name }\nfunc (identifier Identifier) Major() uint32 { return identifier.major }\nfunc MustParseContractWithSemanticErrors[Request, Response any](id string, _ ...string) Contract[Request, Response] {\n\treturn Contract[Request, Response]{id: id}\n}\nfunc (contract Contract[Request, Response]) ID() string { return contract.id }\n")},
		{name: "configuration/configuration.go", data: []byte("package configuration\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"os\"\n\n\t\"github.com/plystra/kernel/plugin/manifest\"\n)\n\nconst MaximumSecretValueBytes = 1 << 20\n\nvar ErrSecretExposure = errors.New(\"Secret serialization is prohibited\")\n\ntype ResolverOptions struct { MaximumValueBytes int }\ntype Resolver struct{}\ntype Secret struct{}\ntype Values struct{}\ntype ObjectMap struct{}\ntype StringMap struct{}\n\nfunc NewResolver(ResolverOptions) (*Resolver, error) { return &Resolver{}, nil }\nfunc LoadDocument(path string) ([]byte, error) { return os.ReadFile(path) }\nfunc (ObjectMap) Names() []string { return nil }\nfunc (ObjectMap) YAML(string) ([]byte, bool) { return nil, false }\nfunc (StringMap) Names() []string { return nil }\nfunc (StringMap) Value(string) (string, bool) { return \"\", false }\nfunc ExtractObjectMap([]byte, string) (ObjectMap, error) { return ObjectMap{}, nil }\nfunc ExtractStringMap([]byte, string) (StringMap, error) { return StringMap{}, nil }\nfunc Decode(context.Context, *Resolver, manifest.Config, []byte) (Values, error) { return Values{}, nil }\n")},
		{name: "go.mod", data: moduleFile},
		{name: "invocation/error.go", data: []byte("package invocation\n\nconst ErrorInternal ErrorCode = \"internal\"\n")},
		{name: "interfaces/kernel/health/v1/interface.go", data: []byte("package healthv1\n\nimport \"context\"\n\nconst ID = \"kernel.health/v1\"\n\n//plystra:interface kernel.health/v1\ntype Interface interface { Health(context.Context, Request) (Response, error) }\n\ntype Request struct{}\nconst StatusHealthy = \"healthy\"\ntype Response struct { Status string `json:\"status\" plystra:\"1,required\"` }\n")},
		{name: "interfaces/kernel/info/v1/interface.go", data: []byte("package infov1\n\nimport \"context\"\n\nconst ID = \"kernel.info/v1\"\n\n//plystra:interface kernel.info/v1\ntype Interface interface { Info(context.Context, Request) (Response, error) }\n\ntype Request struct{}\ntype Response struct { AssemblyAPI string `json:\"assembly_api\" plystra:\"1,required\"`; KernelModule string `json:\"kernel_module\" plystra:\"2,required\"`; KernelVersion string `json:\"kernel_version\" plystra:\"3,required\"` }\n")},
		{name: "intrinsic/intrinsic.go", data: []byte("package intrinsic\n\nimport (\n\t\"context\"\n\t\"os\"\n\n\t\"github.com/plystra/kernel/capability\"\n\thealthv1 \"github.com/plystra/kernel/interfaces/kernel/health/v1\"\n\tinfov1 \"github.com/plystra/kernel/interfaces/kernel/info/v1\"\n\t\"github.com/plystra/kernel/invocation\"\n)\n\ntype BindingOptions struct { ModuleVersion, BuildIdentity string }\n\nvar healthContract = capability.MustParseContractWithSemanticErrors[healthv1.Request, healthv1.Response](\"kernel.health/v1\")\nvar infoContract = capability.MustParseContractWithSemanticErrors[infov1.Request, infov1.Response](\"kernel.info/v1\")\n\nfunc HealthContract() capability.Contract[healthv1.Request, healthv1.Response] { return healthContract }\nfunc InfoContract() capability.Contract[infov1.Request, infov1.Response] { return infoContract }\nfunc NewBindings(BindingOptions) ([]invocation.Binding, error) {\n\thealthEndpoint, err := invocation.NewEndpoint(healthContract, func(context.Context, healthv1.Request) (healthv1.Response, error) {\n\t\tif os.Getenv(\"PLYSTRA_TEST_HEALTH_UNHEALTHY\") == \"1\" { return healthv1.Response{}, nil }\n\t\treturn healthv1.Response{Status: healthv1.StatusHealthy}, nil\n\t})\n\tif err != nil { return nil, err }\n\thealth, err := invocation.NewBinding(invocation.BindingOptions{}, healthEndpoint)\n\tif err != nil { return nil, err }\n\tinfoEndpoint, err := invocation.NewEndpoint(infoContract, func(context.Context, infov1.Request) (infov1.Response, error) { return infov1.Response{}, nil })\n\tif err != nil { return nil, err }\n\tinfo, err := invocation.NewBinding(invocation.BindingOptions{}, infoEndpoint)\n\tif err != nil { return nil, err }\n\treturn []invocation.Binding{health, info}, nil\n}\n")},
		{name: "invocation/invocation.go", data: []byte("package invocation\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"time\"\n\n\t\"github.com/plystra/kernel/capability\"\n)\n\ntype Endpoint struct { id string; invoke func(context.Context, any) (any, error) }\ntype ModuleBuild struct{}\ntype BindingKind string\ntype SelectionReason string\ntype BindingOptions struct {\n\tKind BindingKind\n\tConstructor string\n\tModuleBuild ModuleBuild\n\tSelectionReason SelectionReason\n\tContractDigest [32]byte\n}\ntype Binding struct{ endpoint Endpoint }\ntype Catalog struct { bindings []Binding; byID map[string]Binding }\nconst (\n\tBindingKindIntrinsic BindingKind = \"intrinsic\"\n\tBindingKindImplementation BindingKind = \"implementation\"\n\tSelectionReasonIntrinsic SelectionReason = \"intrinsic\"\n\tSelectionReasonUniqueCompatible SelectionReason = \"unique-compatible\"\n\tSelectionReasonExplicit SelectionReason = \"explicit\"\n)\nfunc NewModuleBuild(string, string, string) (ModuleBuild, error) { return ModuleBuild{}, nil }\nfunc NewEndpoint[Request, Response any](contract capability.Contract[Request, Response], handler capability.Handler[Request, Response]) (Endpoint, error) {\n\treturn Endpoint{id: contract.ID(), invoke: func(ctx context.Context, value any) (any, error) {\n\t\trequest, ok := value.(Request); if !ok { return nil, errors.New(\"request type mismatch\") }\n\t\treturn handler(ctx, request)\n\t}}, nil\n}\nfunc NewBinding(_ BindingOptions, endpoint Endpoint) (Binding, error) { return Binding{endpoint: endpoint}, nil }\nfunc NewCatalog(bindings []Binding) (Catalog, error) {\n\tresult := Catalog{bindings: append([]Binding(nil), bindings...), byID: make(map[string]Binding, len(bindings))}\n\tfor _, binding := range bindings { result.byID[binding.endpoint.id] = binding }\n\treturn result, nil\n}\nfunc (c Catalog) Bindings() []Binding { return append([]Binding(nil), c.bindings...) }\ntype DispatcherOptions struct { DefaultTimeout time.Duration }\ntype Dispatcher struct { published bool; catalog Catalog }\nfunc NewDispatcher(DispatcherOptions) (*Dispatcher, error) { return &Dispatcher{}, nil }\nfunc (d *Dispatcher) Publish(catalog Catalog) error { d.catalog = catalog; d.published = true; return nil }\nfunc (d *Dispatcher) Published() bool { return d != nil && d.published }\ntype Handle[Request, Response any] struct { dispatcher *Dispatcher; id string; available bool }\nfunc NewHandle[Request, Response any](dispatcher *Dispatcher, contract capability.Contract[Request, Response], available bool) (Handle[Request, Response], error) { return Handle[Request, Response]{dispatcher: dispatcher, id: contract.ID(), available: available}, nil }\nfunc (h Handle[Request, Response]) Available() bool { return h.available }\nfunc (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {\n\tvar zero Response\n\tif !h.available || h.dispatcher == nil || !h.dispatcher.published { return zero, errors.New(\"unavailable\") }\n\tbinding, exists := h.dispatcher.catalog.byID[h.id]; if !exists { return zero, errors.New(\"unavailable\") }\n\tvalue, err := binding.endpoint.invoke(ctx, request); if err != nil { return zero, err }\n\tresponse, ok := value.(Response); if !ok { return zero, errors.New(\"response type mismatch\") }\n\treturn response, nil\n}\ntype ErrorCode string\nconst (\n\tErrorInvalidArgument ErrorCode = \"invalid_argument\"\n\tErrorUnauthenticated ErrorCode = \"unauthenticated\"\n\tErrorDenied ErrorCode = \"denied\"\n\tErrorNotFound ErrorCode = \"not_found\"\n\tErrorConflict ErrorCode = \"conflict\"\n\tErrorVersionIncompatible ErrorCode = \"version_incompatible\"\n\tErrorTimeout ErrorCode = \"timeout\"\n\tErrorUnavailable ErrorCode = \"unavailable\"\n\tErrorResultUnknown ErrorCode = \"result_unknown\"\n\tErrorCancelled ErrorCode = \"cancelled\"\n)\nfunc (code ErrorCode) String() string { return string(code) }\nfunc (code ErrorCode) Valid() bool { return code != \"\" }\ntype Error struct { code ErrorCode; detailCode string }\nfunc (*Error) Error() string { return \"invocation error\" }\nfunc (err *Error) Code() ErrorCode { if err == nil { return \"\" }; return err.code }\nfunc (err *Error) DetailCode() string { if err == nil { return \"\" }; return err.detailCode }\ntype SemanticError struct { code string }\nfunc (*SemanticError) Error() string { return \"semantic error\" }\nfunc (err *SemanticError) SemanticErrorCode() string { if err == nil { return \"\" }; return err.code }\nfunc ValidDetailCode(string) bool { return true }\n")},
		{name: "lifecycle/lifecycle.go", data: []byte("package lifecycle\n\nimport (\n\t\"context\"\n\t\"time\"\n)\n\ntype Instance interface {\n\tStart(context.Context) error\n\tStop(context.Context) error\n}\n\ntype State string\nconst (\n\tStateNew State = \"new\"\n\tStateStarting State = \"starting\"\n\tStateRunning State = \"running\"\n\tStateStopping State = \"stopping\"\n\tStateStopped State = \"stopped\"\n\tStateFailed State = \"failed\"\n)\nfunc (state State) Valid() bool { return state == StateNew || state == StateStarting || state == StateRunning || state == StateStopping || state == StateStopped || state == StateFailed }\ntype Binding struct{ instance Instance }\ntype Manager struct{ bindings []Binding; started int; state State }\ntype ManagerOptions struct { RollbackTimeout time.Duration }\n\nfunc NewBinding(_ string, instance Instance) (Binding, error) { return Binding{instance: instance}, nil }\nfunc NewManager(_ ManagerOptions, bindings []Binding) (*Manager, error) { return &Manager{bindings: append([]Binding(nil), bindings...), state: StateNew}, nil }\nfunc (manager *Manager) State() State { return manager.state }\nfunc (manager *Manager) Start(ctx context.Context) error {\n\tmanager.state = StateStarting\n\tfor index := range manager.bindings {\n\t\tif err := manager.bindings[index].instance.Start(ctx); err != nil { manager.state = StateFailed; return err }\n\t\tmanager.started++\n\t}\n\tmanager.state = StateRunning\n\treturn nil\n}\nfunc (manager *Manager) Stop(ctx context.Context) error {\n\tmanager.state = StateStopping\n\tfor index := manager.started - 1; index >= 0; index-- {\n\t\tif err := manager.bindings[index].instance.Stop(ctx); err != nil { manager.state = StateFailed; return err }\n\t\tmanager.started--\n\t}\n\tmanager.state = StateStopped\n\treturn nil\n}\n")},
		{name: "plugin/id.go", data: []byte("package plugin\n\ntype ID struct{ value string }\n\nfunc ParseID(value string) (ID, error) { return ID{value: value}, nil }\nfunc (id ID) String() string { return id.value }\n")},
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
	for _, dependency := range []struct {
		path    string
		version string
	}{
		{path: connectgen.ConnectModulePath, version: connectgen.ConnectModuleVersion},
		{path: connectgen.ProtobufModulePath, version: connectgen.ProtobufModuleVersion},
		{path: "github.com/golang/protobuf", version: "v1.5.0"},
		{path: "github.com/google/go-cmp", version: "v0.7.0"},
		{path: bootstrapgen.YAMLModulePath, version: bootstrapgen.YAMLModuleVersion},
		{path: "gopkg.in/check.v1", version: "v0.0.0-20161208181325-20d25e280405"},
	} {
		copyCachedProxyModule(t, root, dependency.path, dependency.version)
	}
	return root
}

func copyCachedProxyModule(t *testing.T, proxyRoot, modulePath, version string) {
	t.Helper()
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		t.Fatalf("EscapePath(%s): %v", modulePath, err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatalf("EscapeVersion(%s): %v", version, err)
	}
	cacheData, err := gocommand.Output(context.Background(), gocommand.Options{
		Environment: setEnvironmentValue(os.Environ(), "GOWORK", "off"),
	}, "env", "GOMODCACHE")
	if err != nil {
		t.Fatalf("locate Go Module Cache: %v", err)
	}
	cacheRoot := strings.TrimSpace(string(cacheData))
	sourceRoot := filepath.Join(cacheRoot, "cache", "download", filepath.FromSlash(escapedPath), "@v")
	targetRoot := filepath.Join(proxyRoot, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", targetRoot, err)
	}
	for _, suffix := range []string{"list", escapedVersion + ".info", escapedVersion + ".mod", escapedVersion + ".zip", escapedVersion + ".ziphash"} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, suffix))
		if err != nil {
			t.Fatalf("read cached %s@%s %s: %v", modulePath, version, suffix, err)
		}
		writeTestFile(t, filepath.Join(targetRoot, suffix), data)
	}
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
	if declared, exists := source["go.mod"]; exists {
		moduleFile = declared
	}
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

func setEnvironmentValue(environment []string, wanted, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, wanted) {
			result = append(result, entry)
		}
	}
	return append(result, wanted+"="+value)
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
	want := map[string]string{
		"github.com/plystra/kernel": newproject.KernelVersion,
		bootstrapgen.YAMLModulePath: bootstrapgen.YAMLModuleVersion,
	}
	if len(parsed.Require) != len(want) {
		t.Fatalf("requirements = %#v", parsed.Require)
	}
	for _, requirement := range parsed.Require {
		version, exists := want[requirement.Mod.Path]
		if !exists || requirement.Mod.Version != version || requirement.Indirect {
			t.Fatalf("requirements = %#v", parsed.Require)
		}
		delete(want, requirement.Mod.Path)
	}
	if len(want) != 0 {
		t.Fatalf("requirements omit = %#v", want)
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
	if len(parsed.Replace) != 0 {
		t.Fatalf("generated go.mod contains permanent replacements: %#v", parsed.Replace)
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

func assertDefaultTransportScaffold(t *testing.T, configuration []byte) {
	t.Helper()
	const expected = "  transports:\n    connect: true\n    rest: false\n"
	if !bytes.Contains(configuration, []byte(expected)) {
		t.Fatalf("Project configuration omits explicit default transports:\n%s", configuration)
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
		"## Choose the smallest workflow",
		"### Operate a Project created from a template",
		"The current CLI does not advertise any template as qualified",
		"### Change ordinary business behavior",
		"adds two public concepts",
		"the other concrete Implementation package",
		"### Select one environment",
		"Use --config only when the task",
		"one complete replacement document; it is an advanced",
		"## Detailed task reference",
		"Read only the section that matches the current task",
		"## Module and file ownership",
		"plystra new app",
		"plystra new app --module github.com/acme/app",
		"plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3",
		"Template-declared operational values and Secret-reference placeholders",
		"immediate plystra generate --check equivalent",
		"runs the same read-only workflow as plystra check",
		"builds every staged Go package with -mod=readonly",
		"builds generated/go/application with GOWORK=off",
		"invokes intrinsic kernel.health/v1",
		"stops lifecycle providers cleanly",
		"does not read PLATFORM_SMTP_PASSWORD",
		"invent values for required fields omitted by the template",
		"plystra plugin create records",
		"plystra capability create records.read --query --plugin records --expose",
		"plystra implement email.send/v1 --package ./mailer",
		"creates no copied contract",
		"capabilities/records.read/v1/capability.yaml",
		"plugin.yaml",
		"plystra.yaml",
		"## Compose dependency Project configuration",
		"Every direct or transitive",
		"Dependency files such",
		"as plystra.production.yaml and plystra.test.yaml are never inherited",
		"Resolve an inherited Implementation conflict with one exact current-Project choice",
		"plystra use email.send/v1 example.com/acme/email/smtp.New",
		"plystra use email.send/v1 example.com/acme/email/production.New --env production",
		"plystra use email.send/v1 example.com/acme/email/customer.New --config deploy/customer-a.yaml",
		"rolls back every owned file after",
		"Remove only exact inherited composable declarations with sparse edits and null",
		"Dependency exposure is ignored rather than inherited",
		"email.send/v1: null",
		"legacy_host: null",
		"Declared objects merge recursively",
		"Dependency http.expose, http.address, http.transports, http.cors, and",
		"interfaces.use and interfaces.policies replace",
		"Only positive timeout is accepted",
		"Values normalize and replace",
		"Enforcement is deferred",
		"plystra add github.com/acme/email@v1.4.2",
		"plystra remove github.com/acme/email",
		"plystra update github.com/acme/email@v1.5.0",
		"retains the selected module as a direct",
		"preserves an existing direct requirement",
		"only that module query",
		"restores every transaction-owned module",
		"non-secret composition",
		"## Select an environment or one complete current-Project configuration",
		"plystra generate --env production",
		"plystra generate --check --env production",
		"go run ./generated/go/application --env production",
		"Generated startup accepts the same --env selector or PLYSTRA_ENV",
		"--config selector or PLYSTRA_CONFIG",
		"bounded compatibility projection",
		"rebuild with the same",
		"Runtime-only address",
		"go run ./generated/go/application --config deploy/customer-a.yaml",
		"does not merge it beneath",
		"PLYSTRA_ENV supplies the same environment name",
		"plystra capability expose records.read/v1 --env production",
		"plystra capability expose records.read/v1 --config deploy/customer-a.yaml",
		"regenerates with the same selection",
		"http.transports is a closed current-Project object",
		"New Project scaffolds write both fields explicitly",
		"null restores that field's",
		"Dependency Project transport",
		"official generated JavaScript SDK requires connect: true",
		"http.cors is an optional closed current-Project object",
		"requires one nonempty allowed_origins list",
		"http.cors to null",
		"CORS settings are ignored",
		"Generated Connect handlers enforce the policy before",
		"Authorization, Connect-Protocol-Version, Connect-Timeout-Ms",
		"normalized HTTP/HTTPS",
		"at most four",
		"totaling at most 4096",
		"return 403",
		"Without http.cors",
		"reject cross-origin preflight",
		"Do not combine --env and --config",
		"preserves the sparse overlay",
		"plystra generate --config deploy/customer-a.yaml",
		"plystra generate --check --config deploy/customer-a.yaml",
		"PLYSTRA_CONFIG supplies the same path",
		"generated/compatibility/{interfaces,interface-metadata,interface-transport,interface-javascript,interface-documentation}.json",
		"interface-documentation.json records doc kind, path, digest",
		"an empty state",
		"API-documentation generator",
		"Refresh with plystra generate",
		"use plystra generate",
		"plystra generate --check",
		"never edit them.",
		"generated/proto/wire-map.json is durable CLI-owned compatibility history",
		"every visible authored Interface message",
		"exposed or not and even with Connect",
		"Authored positive plystra numbers are wire numbers",
		"rejects renumbering or reuse",
		"permanently reserves removed Protobuf names",
		"Only exposed Connect Interfaces become active",
		"emit schemas",
		"descriptors, handlers, or SDK output",
		"exactly one unary service from every exposed Interface package",
		"procedure path is derived from the exact Interface ID",
		"temporary legacy",
		"owns no competing messages, service, or procedure",
		"Protobuf-derived names must be unique within each request and response",
		"foo1 and foo_1 both derive the ProtoJSON name foo1",
		"http_status and h_t_t_p_status both derive one HTTPStatusEnum type",
		"Protobuf naming collision",
		"Unsupported Connect operation kind",
		"generated/proto/descriptor-set.pb is the self-contained deterministic",
		"A selected Connect surface also emits a Go handler",
		"explicit semantics.kind: query or command",
		"projects each as one unary",
		"event or stream from http.expose",
		"Binary Protobuf requests",
		"limited to 1 MiB",
		"maximum message depth of 64",
		"65,536-node budget",
		"unknown fields at",
		"any message depth",
		"Binary Protobuf responses",
		"same size, depth, and node",
		"bounds. Generated conversion",
		"serializes deterministically",
		"no partial response",
		"ProtoJSON requests independently accept at most 1 MiB",
		"65,536 structural tokens",
		"Unknown or duplicate fields",
		"invalid UTF-8",
		"Optional non-nullable null becomes absence",
		"full-range integers remain exact",
		"ProtoJSON responses use the",
		"same exact generated message and canonical response validation",
		"Canonical and",
		"Alias binary and ProtoJSON paths agree",
		"RootContext receives the live external request context",
		"pre-cancelled direct",
		"earlier caller or trusted-root",
		"Connect-Timeout-Ms deadlines",
		"context.DeadlineExceeded",
		"Cancellation and deadlines are best-effort",
		"@bufbuild/protobuf, @connectrpc/connect, and @connectrpc/connect-web runtime",
		"never receive ConnectError as the public error model",
		"ClientOptions requires credentialPolicy",
		"Cookie uses",
		"fetchCredentials same-origin or include",
		"Bearer is",
		"getAccessToken for one bounded raw token",
		"Fetch omit",
		"fail before dispatch as PlystraError",
		"PlystraError credential_error",
		"without token data",
		"AbortSignal in the second argument",
		"bearer acquisition",
		"in-flight cancellation",
		"reaches fetch",
		"Implementation rollback guarantee",
		"plystra.generated.transport.v1.PlystraErrorDetail",
		"requested_interface_id",
		"canonical_interface_id",
		"inspect its immutable detail; do not parse messages or Connect internals",
		"mismatched, or undeclared detail fails closed to internal",
		"src/descriptors.ts",
		"sends binary Connect requests",
		"versioned canonical constraint",
		"exact contract and",
		"constraint digests",
		"configuration schema v5",
		"current_project_paths",
		"Protobuf wire-map digest",
		"top-level transport_toolchain",
		"embedded go/format",
		"generated Go/npm dependencies",
		"global protoc",
		"hosted generator",
		"environment, or explicit-config mode",
		"root dependency baseline",
		"merged beneath deploy/customer-a.yaml",
		"There is no handwritten provider registration",
		"dependencies.Dependencies",
		"generated/go/dependencies/",
		"generated/go/application entrypoint",
		"npm run typecheck",
		"plystra inspect --format json",
		"plystra generate --check",
		"Diagnostic: PLYSTRA_<AREA>_<CONDITION>",
		"PLYSTRA_IMPLEMENTATION_DECLARATION_INVALID",
		"PLYSTRA_IMPLEMENTATION_CONFIG_INVALID",
		"PLYSTRA_IMPLEMENTATION_REQUIRED_INTERFACE_INVALID",
		"PLYSTRA_IMPLEMENTATION_OPTIONAL_INTERFACE_INVALID",
		"PLYSTRA_IMPLEMENTATION_RESULT_INVALID",
		"PLYSTRA_IMPLEMENTATION_CONFORMANCE_INVALID",
		"PLYSTRA_INTERFACE_DECLARATION_INVALID",
		"PLYSTRA_INTERFACE_CONTRACT_INVALID",
		"PLYSTRA_INTERFACE_METADATA_INVALID",
		"PLYSTRA_INTERFACE_ID_DUPLICATE",
		"PLYSTRA_AUTHORING_PACKAGE_INVALID",
		"PLYSTRA_INTERFACE_CREATE_NAME_INVALID",
		"PLYSTRA_INTERFACE_CREATE_TARGET_EXISTS",
		"PLYSTRA_IMPLEMENTATION_CREATE_INTERFACE_INVALID",
		"PLYSTRA_IMPLEMENTATION_CREATE_INTERFACE_NOT_FOUND",
		"PLYSTRA_IMPLEMENTATION_CREATE_PACKAGE_INVALID",
		"PLYSTRA_IMPLEMENTATION_CREATE_TARGET_EXISTS",
		"PLYSTRA_USE_INTERFACE_INVALID",
		"PLYSTRA_USE_CONSTRUCTOR_INVALID",
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
	for _, unavailable := range []string{"plystra dev", "plystra build"} {
		if strings.Contains(lower, unavailable) {
			t.Fatalf("Plystra skill advertises unavailable command %q:\n%s", unavailable, data)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "plystra", "agents", "openai.yaml"))
	if err != nil || !bytes.Contains(metadata, []byte("Use $plystra")) || !bytes.Contains(metadata, []byte("Go Module, Plugin, Capability, or plystra.yaml")) {
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
