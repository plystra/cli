package interfaceinventory_test

import (
	"archive/zip"
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestDiscoverTransitiveProjectPreparesOnlyPrivateChecksums(t *testing.T) {
	t.Parallel()
	for _, replacement := range []bool{false, true} {
		name := "selected module"
		if replacement {
			name = "versioned replacement"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testPrivateDiscoveryChecksums(t, replacement)
		})
	}
}

func testPrivateDiscoveryChecksums(t *testing.T, replacement bool) {
	t.Helper()
	selectedModule, selectedVersion := "example.com/transitive", "v1.0.0"
	if replacement {
		selectedModule, selectedVersion = "example.com/replacement", "v1.1.0"
	}
	proxy := t.TempDir()
	for module, files := range map[string]map[string]string{
		"example.com/direct": {
			"go.mod":       "module example.com/direct\ngo 1.26\nrequire example.com/transitive v1.0.0\n",
			"plystra.yaml": "template: example.com/transitive\n",
		},
		selectedModule: {
			"go.mod":           "module " + selectedModule + "\ngo 1.26\n",
			"plystra.yaml":     "{}\n",
			"api/interface.go": interfaceSource("api", "dependency.transitive.run/v1", "Run"),
		},
	} {
		version := "v1.0.0"
		if module == selectedModule {
			version = selectedVersion
		}
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			entry, err := writer.Create(module + "@" + version + "/" + path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte(files[path])); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(proxy, filepath.FromSlash(module), "@v")
		writeFile(t, filepath.Join(base, version+".zip"), archive.String())
		writeFile(t, filepath.Join(base, version+".mod"), files["go.mod"])
		writeFile(t, filepath.Join(base, version+".info"), `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
		writeFile(t, filepath.Join(base, "list"), version+"\n")
	}
	proxyPath := filepath.ToSlash(proxy)
	if !strings.HasPrefix(proxyPath, "/") {
		proxyPath = "/" + proxyPath
	}
	proxyURL := (&url.URL{Scheme: "file", Path: proxyPath}).String()
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": proxyURL, "GOSUMDB": "off", "GOMODCACHE": t.TempDir()})
	root := t.TempDir()
	writeProject(t, root, "example.com/app")
	if replacement {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace=example.com/transitive="+selectedModule+"@"+selectedVersion); err != nil {
			t.Fatal(err)
		}
	}
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", "example.com/direct@v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: t.TempDir(), Environment: environment}, "mod", "download", selectedModule+"@"+selectedVersion); err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil || bytes.Contains(sum, []byte(selectedModule+" "+selectedVersion+" h1:")) {
		t.Fatalf("fixture must lack the transitive content checksum: %v", err)
	}
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]map[string]fileState{root: snapshotFiles(t, root), proxy: snapshotFiles(t, proxy)}
	for _, dependency := range dependencies.Projects() {
		before[dependency.Root()] = snapshotFiles(t, dependency.Root())
	}
	for range 2 {
		found, err := interfaceinventory.DiscoverApplication(t.Context(), project, dependencies, interfaceinventory.Options{Environment: environment})
		if err != nil {
			t.Fatal(err)
		}
		if got := interfaceIDs(found.Interfaces()); !reflect.DeepEqual(got, []string{"dependency.transitive.run/v1"}) {
			t.Fatalf("discovered interfaces = %v", got)
		}
		for path, snapshot := range before {
			if !reflect.DeepEqual(snapshot, snapshotFiles(t, path)) {
				t.Fatalf("discovery modified authored or dependency files at %s", path)
			}
		}
	}
}
