package projectstage

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/module"
)

func TestAcquisitionCachePreservesExactGoDownload(t *testing.T) {
	for _, mode := range []string{"cold", "archive", "partial", "complete", "unused-zip", "tampered-zip", "default-permissions", "private-source-drift", "private-metadata-drift", "origin-drift", "wrong-checksum"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, _, cache := fileProxyFixture(t)
			if mode == "default-permissions" {
				stage.environment = replaceEnvironment(stage.environment, "GOFLAGS", "")
			}
			activeProxy(t, stage)
			version := module.Version{Path: proxyDependency, Version: "v1.0.0"}
			originalDir := filepath.Join(cache, filepath.FromSlash(version.String()))
			prefix := filepath.Join(cache, "cache", "download", filepath.FromSlash(version.Path), "@v", version.Version)
			if mode != "cold" && mode != "default-permissions" {
				cacheDownload(t, stage, stage.Environment())
				if mode == "partial" {
					writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".partial", "partial extraction\n")
					if err := os.Chmod(filepath.Join(originalDir, "value.go"), 0o600); err != nil {
						t.Fatal(err)
					}
					writeProxyInput(t, originalDir, "value.go", "partial bytes must not be consumed\n")
				} else if mode != "complete" && mode != "unused-zip" {
					relative, err := filepath.Rel(cache, originalDir)
					if err != nil || !filepath.IsLocal(relative) || relative == "." {
						t.Fatal("source removal escaped cache")
					}
					if err := os.RemoveAll(originalDir); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "unused-zip" {
					writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".zip", "unused invalid archive")
				}
				if mode == "tampered-zip" {
					var data bytes.Buffer
					writer := zip.NewWriter(&data)
					for name, content := range map[string]string{"go.mod": proxyManifest, "value.go": "package dependency\nconst Value = 2\n"} {
						file, err := writer.Create(version.String() + "/" + name)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := file.Write([]byte(content)); err != nil {
							t.Fatal(err)
						}
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".zip", data.String())
				}
			}
			prior := stage.Inputs()
			isolated, err := stage.AcquisitionCache(t.Context(), cache, []module.Version{version})
			warm := mode == "complete" || mode == "unused-zip"
			if err != nil || (isolated == cache) != warm {
				t.Fatalf("cache selection for %s: isolated=%t, %v", mode, isolated != cache, err)
			}
			environment := replaceEnvironment(stage.Environment(), "GOMODCACHE", isolated)
			if mode != "cold" && mode != "default-permissions" {
				environment = replaceEnvironment(environment, "GOPROXY", "off")
			}
			downloaded := cacheDownload(t, stage, environment)
			if !within(isolated, downloaded.Dir) || !within(isolated, downloaded.GoMod) {
				t.Fatal("Go escaped the selected acquisition cache")
			}
			if mode == "partial" {
				data, err := os.ReadFile(filepath.Join(originalDir, "value.go"))
				if err != nil || string(data) != "partial bytes must not be consumed\n" {
					t.Fatal("isolated download repaired the original partial extraction", err)
				}
				if _, err := os.Stat(prefix + ".partial"); err != nil {
					t.Fatal("isolated download removed the original partial marker", err)
				}
			} else if !warm {
				if _, err := os.Lstat(originalDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("isolated download populated the original source directory", err)
				}
			}
			source := Source{Directory: downloaded.Dir, Module: version, Checksum: downloaded.Sum}
			if err := stage.RetainSourceTrees(t.Context(), []string{source.Directory}); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(stage.CheckSources(t.Context()), ErrSourceIntegrity) {
				t.Fatal("pending private source was accepted without checksum authority")
			}
			if mode == "wrong-checksum" {
				source.Checksum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			}
			if mode == "wrong-checksum" || mode == "tampered-zip" {
				if !errors.Is(stage.RetainSources(t.Context(), []Source{source}), ErrSourceIntegrity) {
					t.Fatal("private sources bypassed independent checksum validation")
				}
				return
			}
			metadata := []MetadataInput{{Path: downloaded.GoMod, GoModChecksum: downloaded.GoModSum},
				{Path: strings.TrimSuffix(downloaded.GoMod, ".mod") + ".ziphash", Expected: []byte(downloaded.Sum)}}
			if err := stage.RetainSources(t.Context(), []Source{source}, metadata...); err != nil || stage.CheckSources(t.Context()) != nil {
				t.Fatal("authentic acquired source was rejected", err)
			}
			if mode == "partial" {
				// A later ordinary Go phase may legitimately finish the original
				// extraction. Its progress marker is not immutable authored input.
				cacheDownload(t, stage, replaceEnvironment(stage.Environment(), "GOPROXY", "off"))
				if err := stage.Recheck(t.Context()); err != nil {
					t.Fatal("later original extraction invalidated private acquisition", err)
				}
			}
			if mode == "private-source-drift" || mode == "private-metadata-drift" {
				path := filepath.Join(downloaded.Dir, "value.go")
				if mode == "private-metadata-drift" {
					path = downloaded.GoMod
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				writeProxyInput(t, filepath.Dir(path), filepath.Base(path), "private output changed\n")
				if !errors.Is(stage.Recheck(t.Context()), ErrConcurrentChange) {
					t.Fatal("private cache output drift escaped live checks")
				}
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if !warm {
				if _, err := os.Stat(isolated); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("private acquisition cache survived cleanup", err)
				}
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("temporary acquisition outputs leaked into original input evidence", err)
			}
			if mode == "origin-drift" {
				data, err := os.ReadFile(prefix + ".zip")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(prefix+".zip", prefix+".prior"); err != nil {
					t.Fatal(err)
				}
				writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".zip", string(data))
				if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) || prior.Recheck(t.Context()) != nil {
					t.Fatal("original cache evidence was refreshed or earlier evidence was mutated")
				}
			}
		})
	}
}

func cacheDownload(t *testing.T, stage *Stage, environment []string) (result struct{ Dir, GoMod, Sum, GoModSum string }) {
	t.Helper()
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: environment},
		"mod", "download", "-json", proxyDependency+"@v1.0.0")
	if err != nil || json.Unmarshal(output, &result) != nil || result.Dir == "" || result.Sum == "" {
		t.Fatalf("real Go exact download: %v\n%s", err, output)
	}
	return result
}

func TestAcquisitionCacheRejectsUnsafeInputs(t *testing.T) {
	for _, mode := range []string{"relative", "query", "empty", "cancelled", "closed", "unsafe-seed", "unregistered-source"} {
		t.Run(mode, func(t *testing.T) {
			stage, _, cache := fileProxyFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			versions := []module.Version{{Path: proxyDependency, Version: "v1.0.0"}}
			switch mode {
			case "relative":
				cache = "relative"
			case "query":
				versions[0].Version = "latest"
			case "empty":
				versions = nil
			case "cancelled":
				cancel()
			case "closed":
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
			case "unsafe-seed":
				if err := os.MkdirAll(filepath.Join(cache, "cache/download/example.com/dependency/@v/v1.0.0.info"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "unregistered-source":
				if stage.RetainSourceTrees(ctx, []string{stage.Root()}) == nil || stage.RetainMetadata(ctx, []MetadataInput{{Path: filepath.Join(stage.Root(), "go.mod")}}) == nil {
					t.Fatal("arbitrary stage paths acquired original input authority")
				}
				return
			}
			if _, err := stage.AcquisitionCache(ctx, cache, versions); err == nil {
				t.Fatal("unsafe cache acquisition was accepted")
			}
		})
	}
}
