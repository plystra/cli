package scaffoldplan

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestDependencySourcesRetainConsumedCachedArchive(t *testing.T) {
	for _, discover := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(map[bool]string{false: "prepare", true: "discover"}[discover]+map[bool]string{false: "/missing-tree", true: "/partial"}[partial], func(t *testing.T) {
				t.Parallel()
				stage, archive := cachedArchiveStage(t, partial)
				var err error
				if discover {
					err = retainDependencySources(t.Context(), stage, "")
				} else {
					err = prepareDependencySources(t.Context(), stage, "", []module.Version{{Path: "example.com/dependency", Version: "v1.0.0"}})
				}
				if err != nil {
					t.Fatal(err)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(archive)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(archive, filepath.Join(t.TempDir(), "prior.zip")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(archive, data, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(archive, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("consumed cache archive identity was not retained after cleanup: %v", err)
				}
			})
		}
	}
}

func TestDependencyAcquisitionDoesNotRepairOriginalCache(t *testing.T) {
	for _, discover := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(map[bool]string{false: "prepare", true: "discover"}[discover]+map[bool]string{false: "/missing-tree", true: "/partial"}[partial], func(t *testing.T) {
				t.Parallel()
				stage, archive := cachedArchiveStage(t, partial)
				original := strings.TrimSuffix(archive, ".zip") + ".partial"
				cache := ""
				for _, entry := range stage.Environment() {
					if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
						cache = value
					}
				}
				if cache == "" {
					t.Fatal("missing cache fixture")
				}
				directory := filepath.Join(cache, "example.com", "dependency@v1.0.0")
				var err error
				if discover {
					err = retainDependencySources(t.Context(), stage, "")
				} else {
					err = prepareDependencySources(t.Context(), stage, "", []module.Version{{Path: "example.com/dependency", Version: "v1.0.0"}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if partial {
					if _, err := os.Stat(original); err != nil {
						t.Fatal("download repaired original partial extraction", err)
					}
				} else if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("download populated original extraction", err)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("private outputs leaked into original inputs", err)
				}
			})
		}
	}
}

func cachedArchiveStage(t *testing.T, partial bool) (*projectstage.Stage, string) {
	t.Helper()
	const dependency = "example.com/dependency"
	const manifest = "module " + dependency + "\ngo 1.26\n"
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	for name, data := range map[string]string{"go.mod": manifest, "value.go": "package dependency\nconst Value = 1\n"} {
		file, err := archive.Create(dependency + "@v1.0.0/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + dependency + "/@v/v1.0.0.mod":
			_, _ = io.WriteString(w, manifest)
		case "/" + dependency + "/@v/v1.0.0.info":
			_, _ = io.WriteString(w, `{"Version":"v1.0.0","Time":"2026-07-17T00:00:00Z"}`)
		case "/" + dependency + "/@v/v1.0.0.zip":
			_, _ = w.Write(content.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	root, cache := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example.com/app\ngo 1.26\nrequire " + dependency + " v1.0.0\n", "plystra.yaml": "{}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	environment := os.Environ()
	for key, value := range map[string]string{"GOENV": "off", "GOFLAGS": "-modcacherw", "GOWORK": "off", "GOMODCACHE": cache,
		"GOTOOLCHAIN": "local", "GOPROXY": server.URL, "GOSUMDB": "off", "GOPRIVATE": "", "GONOPROXY": "none", "GONOSUMDB": ""} {
		var filtered []string
		for _, entry := range environment {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, key) {
				filtered = append(filtered, entry)
			}
		}
		environment = append(filtered, key+"="+value)
	}
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "-json", dependency+"@v1.0.0")
	var downloaded struct{ Dir, Zip string }
	if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Zip == "" {
		t.Fatalf("seed cached archive: %v", err)
	}
	server.Close()
	relative, err := filepath.Rel(cache, downloaded.Dir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		t.Fatal("source removal escaped isolated cache")
	}
	if partial {
		if err := os.WriteFile(strings.TrimSuffix(downloaded.Zip, ".zip")+".partial", nil, 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.RemoveAll(downloaded.Dir); err != nil {
		t.Fatal(err)
	}
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	return stage, downloaded.Zip
}
