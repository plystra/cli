package projectstage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
)

func TestFileProxyLatestFallbackRetainsAbsence(t *testing.T) {
	t.Parallel()
	stage, directory, _ := fileProxyFixture(t)
	const version = "v0.0.0-20260717000000-0123456789ab"
	writeProxyInput(t, directory, "list", version+"\n")
	writeProxyInput(t, directory, version+".info", `{"Version":"`+version+`","Time":"2026-07-17T00:00:00Z"}`)
	writeProxyInput(t, directory, version+".mod", proxyManifest)
	activeProxy(t, stage)
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()},
		"list", "-m", "-json", proxyDependency+"@latest")
	var selected struct{ Version string }
	if err != nil || json.Unmarshal(output, &selected) != nil || selected.Version != version {
		t.Fatal("real Go did not resolve the latest fallback", err)
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal("unchanged fallback inputs did not survive cleanup", err)
	}
	writeProxyInput(t, filepath.Dir(directory), "@latest", `{"Version":"v0.0.0-20260718000000-0123456789ab","Time":"2026-07-18T00:00:00Z"}`)
	if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
		t.Fatal("creating the requested latest endpoint did not invalidate the fallback")
	}
}

func TestFileProxyMissingRequestsPreserveExactEvidence(t *testing.T) {
	for _, mode := range []string{"leaf", "archive", "missing-parents", "siblings", "repeat", "ancestor-identity", "after-cleanup", "earlier-inputs"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, directory, _ := fileProxyFixture(t)
			prior := stage.Inputs()
			base := activeProxy(t, stage)
			path := filepath.Join(directory, "v2.0.0.mod")
			request := proxyDependency + "/@v/v2.0.0.mod"
			if mode == "archive" {
				path = filepath.Join(directory, "v2.0.0.zip")
				request = proxyDependency + "/@v/v2.0.0.zip"
			}
			if mode == "missing-parents" || mode == "siblings" || mode == "repeat" {
				path = filepath.Join(directory, "absent", "nested", "v2.0.0.mod")
				request = proxyDependency + "/@v/absent/nested/v2.0.0.mod"
			}
			get := func(want int) {
				t.Helper()
				response, err := http.Get(base + request)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != want || strings.Contains(string(body), "PRIVATE_NEW_MANIFEST") {
					t.Fatal("unexpected missing-input response", response.StatusCode, err)
				}
			}
			get(http.StatusNotFound)
			inputs := stage.Inputs()
			if mode == "siblings" || mode == "repeat" {
				writeProxyInput(t, filepath.Dir(path), "v3.0.0.mod", "unrelated manifest\n")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unrelated materialization invalidated an exact missing request", err)
				}
				if mode == "siblings" {
					return
				}
				for range 4 {
					get(http.StatusNotFound)
				}
				if len(stage.artifacts) != 1 {
					t.Fatal("repeated absence refreshed or duplicated evidence")
				}
				inputs = stage.Inputs()
			}
			if mode == "after-cleanup" {
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ancestor-identity" {
				if err := os.Rename(directory, directory+".prior"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeProxyInput(t, filepath.Dir(path), filepath.Base(path), "PRIVATE_NEW_MANIFEST\n")
			}
			if mode == "earlier-inputs" {
				if err := prior.Recheck(t.Context()); err != nil {
					t.Fatal("later negative evidence changed an earlier snapshot", err)
				}
			}
			if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
				t.Fatal("missing-request evidence was not retained")
			}
			if mode != "after-cleanup" {
				get(http.StatusInternalServerError)
				if stage.StopFileProxy() == nil {
					t.Fatal("changed missing input did not remain a sticky failure")
				}
			}
		})
	}
}

func TestFileProxyFailedResolutionRetainsMissingRequest(t *testing.T) {
	t.Parallel()
	stage, directory, _ := fileProxyFixture(t)
	activeProxy(t, stage)
	_, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()},
		"mod", "download", "-json", proxyDependency+"@v2.0.0")
	if err == nil {
		t.Fatal("missing module version was accepted")
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeProxyInput(t, directory, "v2.0.0.info", `{"Version":"v2.0.0"}`)
	if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
		t.Fatal("failed resolution discarded its requested missing version")
	}
}

func TestFileProxyMissingRequestRejectsCaptureFailures(t *testing.T) {
	for _, mode := range []string{"cancelled", "budget", "non-directory"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, directory, _ := fileProxyFixture(t)
			base := activeProxy(t, stage)
			path := filepath.Join(directory, "absent", "v2.0.0.mod")
			request := proxyDependency + "/@v/absent/v2.0.0.mod"
			if mode == "cancelled" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := stage.retainProxyFile(ctx, path, request); err == nil || errors.Is(err, errProxyNotFound) {
					t.Fatal("cancelled capture became a missing-origin response", err)
				}
			} else {
				if mode == "budget" {
					oldRequest := proxyDependency + "/@v/old-missing.mod"
					oldPath := filepath.Join(directory, "old-missing.mod")
					if _, err := stage.retainProxyFile(t.Context(), oldPath, oldRequest); !errors.Is(err, errProxyNotFound) {
						t.Fatal("could not retain the original negative input", err)
					}
					if _, err := stage.retainProxyFile(t.Context(), filepath.Join(directory, "v1.0.0.mod"), proxyDependency+"/@v/v1.0.0.mod"); err != nil {
						t.Fatal(err)
					}
					// Model an already charged archive inventory without allocating
					// or serving another full bounded archive for this metadata test.
					stage.artifacts[1].inventory = make([]proxyRecord, MaximumEntries)
					if _, err := stage.retainProxyFile(t.Context(), oldPath, oldRequest); !errors.Is(err, errProxyNotFound) {
						t.Fatal("repeated negative input consumed another budget entry", err)
					}
				} else {
					writeProxyInput(t, directory, "absent", "not a directory\n")
				}
				response, err := http.Get(base + request)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusInternalServerError {
					t.Fatal("capture failure became an ordinary missing-origin response", response.StatusCode)
				}
			}
			if stage.StopFileProxy() == nil {
				t.Fatal("missing-input capture failure was not sticky")
			}
		})
	}
}

func TestFileProxyMissingOriginMayShareTemporaryAncestor(t *testing.T) {
	for _, mode := range []string{"existing-root", "missing-root", "private-stage"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			original, _, _ := fileProxyFixture(t)
			if err := original.Close(); err != nil {
				t.Fatal(err)
			}
			// Stage resolves directory aliases, including macOS /var temp paths.
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			origin := parent
			if mode == "missing-root" {
				origin = filepath.Join(parent, "not-published")
			}
			urlPath := filepath.ToSlash(origin)
			if !strings.HasPrefix(urlPath, "/") {
				urlPath = "/" + urlPath
			}
			environment := replaceEnvironment(original.Environment(), "GOPROXY", (&url.URL{Scheme: "file", Path: urlPath}).String())
			stage, err := Create(t.Context(), Options{Start: original.project, Environment: environment, TemporaryParent: parent})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			base := activeProxy(t, stage)
			if mode == "private-stage" {
				name, err := filepath.Rel(origin, filepath.Join(stage.Root(), "go.mod"))
				if err != nil || !filepath.IsLocal(name) {
					t.Fatal("private request escaped the shared origin", err)
				}
				response, err := http.Get(base + filepath.ToSlash(name))
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "example.com/app") || stage.StopFileProxy() == nil {
					t.Fatal("shared origin delivered private staged source", err)
				}
				return
			}
			response, err := http.Get(base + "example.com/missing/@v/v1.0.0.mod")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatal("a shared observation ancestor rejected a legitimate missing origin", response.StatusCode)
			}
			inputs := stage.Inputs()
			if err := stage.StopFileProxy(); err != nil {
				t.Fatal("missing origin poisoned the session", err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("stage cleanup changed the observed ancestor", err)
			}
			writeProxyInput(t, filepath.Join(origin, "example.com", "missing", "@v"), "v1.0.0.mod", "new manifest\n")
			if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
				t.Fatal("shared-ancestor capture discarded its original absence")
			}
		})
	}
}

func writeProxyInput(t *testing.T, root, name, data string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
