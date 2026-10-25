package projectstage

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/module"
)

const proxyDependency = "example.com/dependency"
const proxyManifest = "module " + proxyDependency + "\ngo 1.26\n"

func fileProxyFixture(t *testing.T, overrides ...string) (*Stage, string, string) {
	t.Helper()
	root, proxy, cache := t.TempDir(), t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example.com/app\ngo 1.26\n", "plystra.yaml": "{}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(proxy, filepath.FromSlash(proxyDependency), "@v")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"v1.0.0.mod": []byte(proxyManifest), "v1.0.0.info": []byte(`{"Version":"v1.0.0","Time":"2026-07-17T00:00:00Z"}`),
		"v1.0.0.zip": proxyArchive(t, "v1.0.0"), "list": []byte("v1.0.0\n"),
	} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proxyPath := filepath.ToSlash(proxy)
	if !strings.HasPrefix(proxyPath, "/") {
		proxyPath = "/" + proxyPath
	}
	proxyURL := (&url.URL{Scheme: "file", Path: proxyPath}).String()
	settings := []string{"GOENV=off", "GOWORK=off", "GOFLAGS=-modcacherw", "GOTOOLCHAIN=local", "GOPROXY=" + proxyURL,
		"GOMODCACHE=" + cache, "GOSUMDB=off", "GOPRIVATE=", "GONOPROXY=none", "GONOSUMDB="}
	settings = append(settings, overrides...)
	environment := os.Environ()
	for _, setting := range settings {
		key, value, _ := strings.Cut(setting, "=")
		environment = replaceEnvironment(environment, key, value)
	}
	stage, err := Create(t.Context(), Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	return stage, directory, cache
}

func proxyArchive(t *testing.T, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range [][2]string{{"go.mod", proxyManifest}, {"value.go", "package dependency\nconst Value = 1\n"}} {
		entry, err := writer.Create(proxyDependency + "@" + version + "/" + file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, file[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func activeProxy(t *testing.T, stage *Stage) string {
	t.Helper()
	started, err := stage.StartFileProxy(t.Context(), "")
	if err != nil || !started || stage.proxy == nil {
		t.Fatalf("start file proxy = %t, %v", started, err)
	}
	for _, value := range stage.Environment() {
		if strings.HasPrefix(value, "GOPROXY=") {
			return strings.TrimPrefix(value, "GOPROXY=")
		}
	}
	t.Fatal("no proxy environment")
	return ""
}

func TestFileProxyRealGoCapturesColdPayloadsBeforeDelivery(t *testing.T) {
	t.Parallel()
	stage, directory, cache := fileProxyFixture(t)
	prior := stage.Inputs()
	activeProxy(t, stage)
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "mod", "download", "-json", proxyDependency+"@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var downloaded struct{ Sum string }
	if json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" {
		t.Fatalf("invalid completed acquisition: %s", output)
	}
	if len(stage.artifacts) != 3 {
		t.Fatalf("captured %d origin payloads", len(stage.artifacts))
	}
	var archive *proxyArtifact
	for index := range stage.artifacts {
		if strings.HasSuffix(stage.artifacts[index].request, ".zip") {
			archive = &stage.artifacts[index]
		}
	}
	if archive == nil || len(archive.inventory) != 2 || archive.checksum != downloaded.Sum {
		t.Fatal("frozen archive inventory does not match Go's content identity")
	}
	if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(proxyDependency)+"@v1.0.0", "value.go")); err != nil {
		t.Fatal("Go did not consume and extract the captured archive", err)
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "v1.0.0.zip")
	if err := os.Rename(path, path+".prior"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.file.data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
		t.Fatal("same-byte origin replacement lost after cleanup", err)
	}
	if err := prior.Recheck(t.Context()); err != nil {
		t.Fatal("earlier snapshot acquired later membership", err)
	}
}

func TestFileProxyCapturesManifestWithoutFetchingArchive(t *testing.T) {
	t.Parallel()
	stage, directory, _ := fileProxyFixture(t)
	base := activeProxy(t, stage)
	response, err := http.Get(base + proxyDependency + "/@v/v1.0.0.mod")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// Successful response headers cannot become observable before publication.
	inputs := stage.Inputs()
	if response.StatusCode != http.StatusOK || len(inputs.captured().artifacts) != 1 || len(stage.artifacts[0].inventory) != 0 {
		t.Fatal("payload delivered without the exact demand capture")
	}
	if err := os.WriteFile(filepath.Join(directory, "v1.0.0.mod"), []byte("changed origin"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != proxyManifest {
		t.Fatal("response reread live origin bytes", err)
	}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
		t.Fatal("delivered origin was not retained", err)
	}
}

func TestFileProxyRejectsInvalidArchiveBeforeSuccessfulDelivery(t *testing.T) {
	t.Parallel()
	stage, directory, cache := fileProxyFixture(t)
	if err := os.WriteFile(filepath.Join(directory, "v1.0.0.zip"), []byte("PRIVATE_INVALID_ARCHIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	activeProxy(t, stage)
	_, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "mod", "download", "-json", proxyDependency+"@v1.0.0")
	if err == nil || !errors.Is(stage.StopFileProxy(), ErrSourceIntegrity) {
		t.Fatal("invalid archive was accepted", err)
	}
	if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(proxyDependency)+"@v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected archive was extracted")
	}
	inputs := stage.Inputs()
	if len(inputs.captured().artifacts) != 3 {
		t.Fatal("completed rejected origin evidence was discarded")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal("failure evidence depends on the deleted stage", err)
	}
	for _, value := range []any{inputs, &inputs, struct{ Value Inputs }{inputs}} {
		if formatted := fmt.Sprintf("%#v", value); strings.Contains(formatted, "PRIVATE_INVALID_ARCHIVE") || strings.Contains(formatted, directory) {
			t.Fatal("private acquired payload escaped formatting")
		}
	}
}

func TestFileProxyConcurrentDeliveryAndSnapshots(t *testing.T) {
	t.Parallel()
	stage, _, _ := fileProxyFixture(t)
	base := activeProxy(t, stage)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			response, err := http.Get(base + proxyDependency + "/@v/v1.0.0.mod")
			if err != nil {
				t.Error(err)
				return
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || string(data) != proxyManifest {
				t.Error("concurrent frozen delivery failed", err)
			}
			if err := stage.Inputs().Recheck(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if len(stage.artifacts) != 1 {
		t.Fatal("repeated request refreshed or duplicated origin evidence")
	}
}

func TestFileProxyLeavesUnsupportedAcquisitionModesUnchanged(t *testing.T) {
	for _, configuration := range [][]string{{"GOPROXY=off"}, {"GOPROXY=https://proxy.golang.org,direct"}, {"GOTOOLCHAIN=auto"}, {"GOPROXY=file:///not-present|off"}} {
		t.Run(strings.Join(configuration, ";"), func(t *testing.T) {
			t.Parallel()
			stage, _, _ := fileProxyFixture(t, configuration...)
			before := strings.Join(stage.Environment(), "\n")
			if _, err := stage.StartFileProxy(t.Context(), ""); err != nil || stage.proxy != nil || strings.Join(stage.Environment(), "\n") != before {
				t.Fatal("unsupported acquisition settings were rewritten", err)
			}
		})
	}
}

func TestFileProxyFailureDoesNotDeliverPrivatePayload(t *testing.T) {
	for _, failure := range []string{"drift", "oversized", "symbolic"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			stage, directory, _ := fileProxyFixture(t)
			base := activeProxy(t, stage)
			path := filepath.Join(directory, "v1.0.0.mod")
			switch failure {
			case "drift":
				if err := os.WriteFile(filepath.Join(stage.project, "go.mod"), []byte("PRIVATE_CHANGED_PROJECT"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maximumMetadataBytes+1), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symbolic":
				if err := os.Rename(path, path+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".target", path); err != nil {
					t.Skip("symlink unavailable", err)
				}
			}
			response, err := http.Get(base + proxyDependency + "/@v/v1.0.0.mod")
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusInternalServerError || bytes.Contains(data, []byte(proxyManifest)) {
				t.Fatal("rejected acquisition delivered origin data", err)
			}
			if err := stage.StopFileProxy(); err == nil {
				t.Fatal("capture rejection did not remain fatal to the session")
			}
		})
	}
}

func TestFileProxyAlreadyCancelledStartDoesNotListen(t *testing.T) {
	stage, _, _ := fileProxyFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := stage.StartFileProxy(ctx, ""); err == nil || stage.proxy != nil {
		t.Fatal("cancelled setup started a broker", err)
	}
}

func TestFileProxyRetainsEarlierPayloadsAfterFailedGoCommand(t *testing.T) {
	t.Parallel()
	stage, _, _ := fileProxyFixture(t)
	activeProxy(t, stage)
	_, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()},
		"mod", "download", "-json", proxyDependency+"@v1.0.0", "example.com/missing@v1.0.0")
	if err == nil {
		t.Fatal("missing module was accepted")
	}
	inputs := stage.Inputs()
	if len(inputs.captured().artifacts) != 3 {
		t.Fatal("failed resolution discarded earlier demand captures")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal("failed-command evidence depends on temporary files", err)
	}
}

func TestFileProxyCaptureDoesNotGrantChecksumAuthority(t *testing.T) {
	t.Parallel()
	stage, _, cache := fileProxyFixture(t)
	// Go must still reject this original archive authority after accepting the
	// independently captured manifest and syntactically valid archive.
	if err := os.WriteFile(filepath.Join(stage.Root(), "go.sum"), []byte(proxyDependency+" v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	activeProxy(t, stage)
	_, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "mod", "download", "-json", proxyDependency+"@v1.0.0")
	if err == nil || len(stage.artifacts) != 3 {
		t.Fatal("capture bypassed original checksum authority", err)
	}
	if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(proxyDependency)+"@v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unauthenticated archive was extracted")
	}
	if err := stage.StopFileProxy(); err != nil {
		t.Fatal("broker confused raw capture with Go authentication", err)
	}
}

func TestFileProxyRejectsUnsafeArchiveInventory(t *testing.T) {
	for _, kind := range []string{"path", "crc", "file-bound", "collision"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			stage, directory, _ := fileProxyFixture(t)
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			name := proxyDependency + "@v1.0.0/value.go"
			if kind == "path" {
				name = proxyDependency + "@v1.0.0/../outside"
			}
			if kind == "crc" || kind == "file-bound" {
				size := uint64(1)
				if kind == "file-bound" {
					size = MaximumFileBytes + 1
				}
				entry, err := writer.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, CRC32: 1, CompressedSize64: 1, UncompressedSize64: size})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			} else {
				count := 1
				if kind == "collision" {
					count = 2
				}
				for range count {
					entry, err := writer.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(entry, "package dependency\n"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "v1.0.0.zip"), buffer.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			base := activeProxy(t, stage)
			response, err := http.Get(base + proxyDependency + "/@v/v1.0.0.zip")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusInternalServerError || stage.StopFileProxy() == nil {
				t.Fatal("unsafe archive inventory was delivered")
			}
		})
	}
}

func TestFileProxyStickyFailureRemainsBounded(t *testing.T) {
	stage, directory, _ := fileProxyFixture(t)
	if err := os.WriteFile(filepath.Join(directory, "v1.0.0.zip"), []byte("invalid zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := activeProxy(t, stage)
	for index := range 40 {
		name := "v1.0.0.mod"
		if index == 0 {
			name = "v1.0.0.zip"
		}
		response, err := http.Get(base + proxyDependency + "/@v/" + name)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatal("later acquisition bypassed sticky failure")
		}
	}
	err := stage.StopFileProxy()
	if err == nil || len(err.Error()) > 1_024 {
		t.Fatal("repeated requests expanded the fatal error tree")
	}
}

func TestFileProxyToolchainDistributionKeepsOriginalScheme(t *testing.T) {
	stage, directory, _ := fileProxyFixture(t)
	toolchain := filepath.Join(directory, "..", "..", "..", "golang.org", "toolchain")
	if err := os.MkdirAll(toolchain, 0o700); err != nil {
		t.Fatal(err)
	}
	before := strings.Join(stage.Environment(), "\n")
	if _, err := stage.StartFileProxy(t.Context(), ""); err != nil || stage.proxy != nil || strings.Join(stage.Environment(), "\n") != before {
		t.Fatal("explicit toolchain distribution lost the file-proxy exception", err)
	}
}

func TestFileProxyRejectsNewToolchainDistribution(t *testing.T) {
	stage, directory, _ := fileProxyFixture(t)
	activeProxy(t, stage)
	if err := os.MkdirAll(filepath.Join(directory, "..", "..", "..", "golang.org", "toolchain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stage.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
		t.Fatal("toolchain exclusion changed after proxy selection", err)
	}
}

func TestFileProxyStartCloseCannotLeaveListener(t *testing.T) {
	for range 8 {
		stage, _, _ := fileProxyFixture(t)
		var group sync.WaitGroup
		group.Go(func() { _, _ = stage.StartFileProxy(t.Context(), "") })
		group.Go(func() {
			if err := stage.Close(); err != nil {
				t.Error(err)
			}
		})
		group.Wait()
		for _, value := range stage.Environment() {
			if !strings.HasPrefix(value, "GOPROXY=http://127.0.0.1:") {
				continue
			}
			selected, err := url.Parse(strings.TrimPrefix(value, "GOPROXY="))
			if err != nil {
				t.Fatal(err)
			}
			connection, err := net.DialTimeout("tcp", selected.Host, time.Second)
			if err == nil {
				connection.Close()
				t.Fatal("closed stage retained its acquisition listener")
			}
		}
	}
}

func TestFileProxyArchiveDirectoriesMatchGoAndConsumeBudget(t *testing.T) {
	stage, directory, _ := fileProxyFixture(t)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	original := proxyArchive(t, "v1.0.0")
	reader, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if err := writer.Copy(file); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := writer.Create(proxyDependency + "@v1.0.0/empty/"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "v1.0.0.zip"), buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := proxyArtifact{module: module.Version{Path: proxyDependency, Version: "v1.0.0"}, file: fileSnapshot{data: buffer.Bytes()}}
	if err := stage.checkProxyArchive(t.Context(), &artifact, budget{entries: MaximumEntries - 3}); err == nil {
		t.Fatal("directory ZIP record bypassed shared entry budget")
	}
	activeProxy(t, stage)
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "mod", "download", "-json", proxyDependency+"@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var downloaded struct{ Sum string }
	if json.Unmarshal(output, &downloaded) != nil {
		t.Fatal("invalid Go download output")
	}
	for _, acquired := range stage.artifacts {
		if strings.HasSuffix(acquired.request, ".zip") {
			if len(acquired.inventory) != 4 || acquired.checksum != downloaded.Sum {
				t.Fatal("duplicate directory ZIP content identity differs from Go")
			}
			retained := Stage{artifacts: []proxyArtifact{acquired}}
			if retained.inputBudget().entries != 5 {
				t.Fatal("retained budget collapsed duplicate directory records")
			}
		}
	}
}
