package scaffoldplan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestPrepareDependencySourcesRetainsCancelledMetadata(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		for _, extension := range []string{".mod", ".info"} {
			t.Run(fmt.Sprintf("%s/cached=%t", extension, seeded), func(t *testing.T) {
				t.Parallel()
				const dependency = "example.com/dependency"
				const manifest = "module " + dependency + "\ngo 1.26\n"
				checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(manifest)), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/" + dependency + "/@v/v1.0.0.mod":
						_, _ = io.WriteString(w, manifest)
					case "/" + dependency + "/@v/v1.0.0.info":
						_, _ = io.WriteString(w, "{\"Version\":\"v1.0.0\",\"Time\":\"2026-07-17T00:00:00Z\"}\n")
					case "/" + dependency + "/@v/v1.0.0.zip":
						cancel()
						<-r.Context().Done()
					default:
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(server.Close)
				root, cache := t.TempDir(), t.TempDir()
				if seeded {
					// Positive originals must survive a later interrupted private
					// download. Cold private outputs are not original cache files.
					directory := filepath.Join(cache, "cache", "download", "example.com", "dependency", "@v")
					if err := os.MkdirAll(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					for suffix, data := range map[string]string{".mod": manifest, ".info": "{\"Version\":\"v1.0.0\",\"Time\":\"2026-07-17T00:00:00Z\"}\n"} {
						if err := os.WriteFile(filepath.Join(directory, "v1.0.0"+suffix), []byte(data), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				files := map[string]string{"go.mod": "module example.com/app\ngo 1.26\n", "plystra.yaml": "{}\n",
					"go.sum": dependency + " v1.0.0/go.mod " + checksum + "\n"}
				for name, data := range files {
					if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				overrides := map[string]string{"GOENV": "off", "GOFLAGS": "-modcacherw", "GOWORK": "off", "GOMODCACHE": cache,
					"GOTOOLCHAIN": "local", "GOPROXY": server.URL, "GOSUMDB": "off", "GOPRIVATE": "", "GONOPROXY": "none", "GONOSUMDB": ""}
				var environment []string
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
						environment = append(environment, entry)
					}
				}
				for key, value := range overrides {
					environment = append(environment, key+"="+value)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, Environment: environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				err = prepareDependencySources(ctx, stage, "", []module.Version{{Path: dependency, Version: "v1.0.0"}})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("interrupted adapter lost cancellation: %v", err)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("unchanged interrupted evidence rejected: %v", err)
				}
				path := filepath.Join(cache, "cache", "download", "example.com", "dependency", "@v", "v1.0.0"+extension)
				if seeded {
					if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
						t.Fatal(err)
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("adapter did not retain interrupted %s evidence: %v", extension, err)
					}
				} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cancelled private download materialized original metadata", err)
				}
				for name, data := range files {
					after, err := os.ReadFile(filepath.Join(root, name))
					if err != nil || string(after) != data {
						t.Fatalf("interrupted adapter changed authored %s", name)
					}
				}
			})
		}
	}
}
