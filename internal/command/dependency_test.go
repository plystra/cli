package command_test

import (
	"archive/zip"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func TestRunAddComposesDependencyProjectAndPreservesUnselectedConfiguration(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		{
			path:     "example.com/acme/platform",
			version:  "v1.0.0",
			manifest: "capabilities:\n  require: [kernel.health/v1]\n",
		},
	})
	environment := commandDependencyEnvironment(t, proxy)
	root := writeCapabilityCommandModule(t)
	rootData := "# Shared application choices.\n{}\n"
	overlayData := "# Unselected production choices.\ncapabilities:\n  require: {add: [kernel.info/v1]}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayData)
	proxyBefore := commandTree(t, proxy)

	query := "example.com/acme/platform@v1.0.0"
	exitCode, stdout, stderr := runCommand(t, []string{"add", query}, filepath.Join(root, "records"), environment)
	wantOutput := "added dependency " + query + " to example.com/acme/library in " + root + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("plystra add = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	goMod := string(readCommandFile(t, root, "go.mod"))
	if !strings.Contains(goMod, "example.com/acme/platform v1.0.0") {
		t.Fatalf("go.mod omits selected dependency:\n%s", goMod)
	}
	parsedModule, err := modfile.Parse("go.mod", []byte(goMod), nil)
	if err != nil {
		t.Fatalf("Parse(go.mod): %v", err)
	}
	foundDirect := false
	for _, requirement := range parsedModule.Require {
		if requirement.Mod.Path == "example.com/acme/platform" && requirement.Mod.Version == "v1.0.0" && !requirement.Indirect {
			foundDirect = true
		}
	}
	if !foundDirect {
		t.Fatalf("go.mod does not retain platform as a direct dependency:\n%s", goMod)
	}
	rootConfiguration := string(readCommandFile(t, root, "plystra.yaml"))
	if !strings.Contains(rootConfiguration, "# Shared application choices.") || !strings.Contains(rootConfiguration, "kernel.health/v1") {
		t.Fatalf("root dependency composition = %q", rootConfiguration)
	}
	if got := string(readCommandFile(t, root, "plystra.production.yaml")); got != overlayData {
		t.Fatalf("add rewrote unselected overlay: %q", got)
	}
	for _, generated := range []string{
		"generated/.plystra-manifest.json",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/clients/kernel/health/v1/client_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
		"generated/manifest.json",
	} {
		assertCommandFile(t, root, generated)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("post-add generate check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if proxyAfter := commandTree(t, proxy); !reflect.DeepEqual(proxyAfter, proxyBefore) {
		t.Fatalf("Go Module proxy changed during add:\nbefore: %#v\nafter:  %#v", proxyBefore, proxyAfter)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunAddRestoresModuleConfigurationAndGeneratedOutputAfterValidationFailure(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		{
			path:     "example.com/acme/platform",
			version:  "v1.0.0",
			manifest: "capabilities:\n  require: [kernel.health/v1]\n",
		},
	})
	environment := commandDependencyEnvironment(t, proxy)
	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "# Preserve root.\n{}\n")
	exitCode, stdout, stderr := runCommand(t, []string{"capability", "create", "records.list", "--plugin", "records"}, root, environment)
	if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "created capability records.list/v1") {
		t.Fatalf("initial capability creation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(root, "validation_failure_test.go"), `package library

import "testing"

func TestInjectedDependencyValidationFailure(t *testing.T) {
	t.Fatal("injected dependency validation failure")
}
`)
	before := commandTree(t, root)
	query := "example.com/acme/platform@v1.0.0"
	exitCode, stdout, stderr = runCommand(t, []string{"add", query}, filepath.Join(root, "records"), environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "injected dependency validation failure") {
		t.Fatalf("failed plystra add = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed add changed Project-owned files:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunAddRejectsInvalidModuleQueryBeforeMutation(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"add", "../outside@v1.0.0"}, filepath.Join(root, "records"), commandGoEnvironment())
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "Go Module path") {
		t.Fatalf("invalid plystra add = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid query changed Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunRemoveRecomposesProjectAndPreservesUnselectedConfiguration(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		{
			path:     "example.com/acme/platform",
			version:  "v1.0.0",
			manifest: "capabilities:\n  require: [kernel.health/v1]\n",
		},
	})
	environment := commandDependencyEnvironment(t, proxy)
	root := writeCapabilityCommandModule(t)
	rootData := "# Shared application choices.\n{}\n"
	overlayData := "# Unselected production choices.\ncapabilities:\n  require: {add: [kernel.info/v1]}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayData)
	proxyBefore := commandTree(t, proxy)

	query := "example.com/acme/platform@v1.0.0"
	exitCode, stdout, stderr := runCommand(t, []string{"add", query}, root, environment)
	if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "added dependency "+query) {
		t.Fatalf("initial plystra add = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	modulePath := "example.com/acme/platform"
	exitCode, stdout, stderr = runCommand(t, []string{"remove", modulePath}, filepath.Join(root, "records"), environment)
	wantOutput := "removed dependency " + modulePath + " from example.com/acme/library in " + root + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("plystra remove = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	goMod := string(readCommandFile(t, root, "go.mod"))
	if strings.Contains(goMod, modulePath) {
		t.Fatalf("go.mod retains removed dependency:\n%s", goMod)
	}
	rootConfiguration := string(readCommandFile(t, root, "plystra.yaml"))
	if !strings.Contains(rootConfiguration, "# Shared application choices.") || strings.Contains(rootConfiguration, "kernel.health/v1") {
		t.Fatalf("root dependency recomposition = %q", rootConfiguration)
	}
	if got := string(readCommandFile(t, root, "plystra.production.yaml")); got != overlayData {
		t.Fatalf("remove rewrote unselected overlay: %q", got)
	}
	for _, obsolete := range []string{
		"generated/go/clients/kernel/health/v1/client_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
	} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(obsolete))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed dependency output %s still exists: %v", obsolete, err)
		}
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("post-remove generate check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if proxyAfter := commandTree(t, proxy); !reflect.DeepEqual(proxyAfter, proxyBefore) {
		t.Fatalf("Go Module proxy changed during remove:\nbefore: %#v\nafter:  %#v", proxyBefore, proxyAfter)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunRemoveRestoresModuleConfigurationAndGeneratedOutputAfterValidationFailure(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		{
			path:     "example.com/acme/platform",
			version:  "v1.0.0",
			manifest: "capabilities:\n  require: [kernel.health/v1]\n",
		},
	})
	environment := commandDependencyEnvironment(t, proxy)
	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "# Preserve root.\n{}\n")
	query := "example.com/acme/platform@v1.0.0"
	exitCode, stdout, stderr := runCommand(t, []string{"add", query}, root, environment)
	if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "added dependency "+query) {
		t.Fatalf("initial plystra add = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(root, "validation_failure_test.go"), `package library

import "testing"

func TestInjectedDependencyRemovalValidationFailure(t *testing.T) {
	t.Fatal("injected dependency removal validation failure")
}
`)
	before := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"remove", "example.com/acme/platform"}, filepath.Join(root, "records"), environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "injected dependency removal validation failure") {
		t.Fatalf("failed plystra remove = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed remove changed Project-owned files:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunRemoveRejectsInvalidOrUnselectedModuleBeforeMutation(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	for _, test := range []struct {
		name       string
		modulePath string
		wantError  string
	}{
		{name: "version query", modulePath: "example.com/acme/platform@v1.0.0", wantError: "without a version query"},
		{name: "unsafe path", modulePath: "../outside", wantError: "Go Module path"},
		{name: "unselected path", modulePath: "example.com/acme/platform", wantError: "is not selected in go.mod"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"remove", test.modulePath}, filepath.Join(root, "records"), commandGoEnvironment())
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.wantError) {
				t.Fatalf("invalid plystra remove = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid removal changed Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

type commandProxyModule struct {
	path     string
	version  string
	manifest string
}

func writeCommandDependencyProxy(t *testing.T, modules []commandProxyModule) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proxy")
	for _, candidate := range modules {
		escapedPath, err := module.EscapePath(candidate.path)
		if err != nil {
			t.Fatalf("EscapePath(%s): %v", candidate.path, err)
		}
		escapedVersion, err := module.EscapeVersion(candidate.version)
		if err != nil {
			t.Fatalf("EscapeVersion(%s): %v", candidate.version, err)
		}
		versionRoot := filepath.Join(root, filepath.FromSlash(escapedPath), "@v")
		if err := os.MkdirAll(versionRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", versionRoot, err)
		}
		writeCommandFile(t, filepath.Join(versionRoot, "list"), candidate.version+"\n")
		writeCommandFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-07-19T00:00:00Z\"}\n", candidate.version))
		goMod := []byte("module " + candidate.path + "\n\ngo 1.26\n")
		writeCommandFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), string(goMod))

		archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
		if err != nil {
			t.Fatalf("Create(zip): %v", err)
		}
		archive := zip.NewWriter(archiveFile)
		prefix := candidate.path + "@" + candidate.version + "/"
		files := []struct {
			name string
			data []byte
		}{
			{name: "go.mod", data: goMod},
			{name: "plystra.yaml", data: []byte(candidate.manifest)},
			{name: "project.go", data: []byte("package project\n")},
		}
		for _, file := range files {
			header := &zip.FileHeader{Name: prefix + file.name, Method: zip.Deflate}
			header.SetMode(0o444)
			header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
			writer, err := archive.CreateHeader(header)
			if err != nil {
				_ = archive.Close()
				_ = archiveFile.Close()
				t.Fatalf("CreateHeader(%s): %v", file.name, err)
			}
			if _, err := writer.Write(file.data); err != nil {
				_ = archive.Close()
				_ = archiveFile.Close()
				t.Fatalf("Write(%s): %v", file.name, err)
			}
		}
		if err := archive.Close(); err != nil {
			_ = archiveFile.Close()
			t.Fatalf("Close(zip): %v", err)
		}
		if err := archiveFile.Close(); err != nil {
			t.Fatalf("Close(zip file): %v", err)
		}
	}
	return root
}

func commandDependencyEnvironment(t *testing.T, proxyRoot string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxyRoot)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	return commandGoEnvironmentWith(map[string]string{
		"GOFLAGS":   "-modcacherw",
		"GONOPROXY": "none",
		"GONOSUMDB": "",
		"GOPRIVATE": "",
		"GOPROXY":   (&url.URL{Scheme: "file", Path: proxyPath}).String(),
		"GOSUMDB":   "off",
	})
}
