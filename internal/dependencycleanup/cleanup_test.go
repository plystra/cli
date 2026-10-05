package dependencycleanup_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/plystra/cli/internal/dependencycleanup"
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
  bind:
    implementations:
      example.com/dep/provider.New:
        old: primary
        retained: primary
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), original)
	environment := providerEnvironment(t, proxy)
	runGo(t, root, environment, "get", "example.com/dep@v1.0.0")
	writeProviderVersion(t, root, environment, "v1.1.0")

	plan, err := dependencycleanup.Plan(context.Background(), dependencycleanup.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Changed {
		t.Fatal("Plan reported no ownership change")
	}
	want := `resources:
  instances:
    primary:
      use: example.com/dep/provider.New
  bind:
    implementations:
      example.com/dep/provider.New:
        retained: primary
`
	if !bytes.Equal(plan.Data, []byte(want)) {
		t.Fatalf("planned document = %q, want %q", plan.Data, want)
	}
	if got := string(readFile(t, filepath.Join(root, "plystra.yaml"))); got != original {
		t.Fatalf("Plan mutated selected document: %q", got)
	}
}

func writeProviderProxy(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proxy")
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
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
		writeModuleZip(t, root, "example.com/dep", version, map[string]string{
			"go.mod":               "module example.com/dep\n\ngo 1.26\n",
			"api/api.go":           "package api\n//plystra:resource storage.database/v1\ntype Resource interface { Ping() }\n",
			"provider/provider.go": provider,
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
