package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestDiscoverRetainsOnlyDemandedGraphManifestsBeforeListing(t *testing.T) {
	a1 := module.Version{Path: "example.com/graph/a", Version: "v1.0.0"}
	a2 := module.Version{Path: a1.Path, Version: "v1.1.0"}
	a3 := module.Version{Path: a1.Path, Version: "v1.2.0"}
	b1 := module.Version{Path: "example.com/graph/b", Version: "v1.0.0"}
	b2 := module.Version{Path: b1.Path, Version: "v1.1.0"}
	c1 := module.Version{Path: "example.com/graph/c", Version: "v1.0.0"}
	c2 := module.Version{Path: c1.Path, Version: "v1.1.0"}
	c3 := module.Version{Path: c1.Path, Version: "v1.2.0"}
	d1 := module.Version{Path: "example.com/graph/d", Version: "v1.0.0"}
	z1 := module.Version{Path: "example.com/graph/z", Version: "v1.0.0"}
	x1 := module.Version{Path: "example.com/graph/x", Version: "v1.0.0"}
	unused := module.Version{Path: "example.com/graph/unused", Version: "v1.0.0"}
	type graphModule struct {
		mod      module.Version
		goLine   string
		requires []module.Version
	}
	node := func(mod module.Version, goLine string, requires ...module.Version) graphModule {
		return graphModule{mod, goLine, requires}
	}
	for _, test := range []struct {
		name, mainGo, workGo            string
		workspace, fail                 bool
		roots, memberRoots, want        []module.Version
		cold, unrecorded, poison, strip []module.Version
		modules                         []graphModule
		replacements                    []string
		altered                         map[module.Version]string
		local                           string
		exclusions                      []module.Version
		callback                        string
		batches                         []int
		rawRequirement                  bool
		direct                          bool
		memberReplacements              []string
		wantInformation                 []module.Version
		informationOnly                 bool
		informationMode                 string
		memberPath                      string
		wantSources                     []module.Version
		sourceMode                      string
	}{
		{name: "sources-pruned", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}, wantSources: []module.Version{a1, b1}},
		{name: "sources-legacy", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}, wantSources: []module.Version{a1, b1, c1}},
		{name: "sources-workspace-upgrade", workspace: true, roots: []module.Version{a1, d1}, memberRoots: []module.Version{a3}, modules: []graphModule{node(d1, "1.17", a2), node(a3, "1.17", c1)}, want: []module.Version{a1, d1, a3, c1}, wantSources: []module.Version{a3, d1, c1}},
		{name: "sources-readonly-rejection", fail: true, roots: []module.Version{a1, b1}, modules: []graphModule{node(b1, "1.17", a2), node(a2, "1.17", z1)}, want: []module.Version{a1, b1, a2}, wantSources: []module.Version{a2, b1}},
		{name: "sources-replacement", roots: []module.Version{a1}, replacements: []string{a1.Path + " => " + c1.String(), a1.String() + " => " + d1.String()}, modules: []graphModule{node(d1, "1.17", b1)}, want: []module.Version{d1, b1}, wantSources: []module.Version{d1, b1}},
		{name: "sources-versioned-main-target", workspace: true, memberPath: b1.Path, roots: []module.Version{a1}, replacements: []string{a1.String() + " => " + b1.String()}, want: []module.Version{b1}, wantSources: []module.Version{b1}},
		{name: "sources-local", roots: []module.Version{a1}, replacements: []string{a1.String() + " => ../local"}, local: "module " + a1.Path + "\ngo 1.17\nrequire " + b1.Path + " " + b1.Version + "\n", want: []module.Version{b1}, wantSources: []module.Version{b1}},
		{name: "sources-concurrent-replacements", roots: []module.Version{a1}, replacements: []string{x1.String() + " => " + b1.String(), b1.String() + " => " + c1.String()}, modules: []graphModule{node(a1, "1.17", x1, b1)}, want: []module.Version{a1, b1}, wantSources: []module.Version{a1, b1, c1}},
		{name: "sources-shared-target", roots: []module.Version{a1}, replacements: []string{x1.String() + " => " + b1.String(), z1.String() + " => " + b1.String()}, modules: []graphModule{node(a1, "1.17", x1, z1)}, want: []module.Version{a1, b1}, wantSources: []module.Version{a1, b1}},
		{name: "sources-unrecorded-selected-manifest", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, unrecorded: []module.Version{b1}, want: []module.Version{a1}, wantSources: []module.Version{a1}},
		{name: "sources-cold-frontier", roots: []module.Version{a1}, cold: []module.Version{a1}, wantSources: []module.Version{}},
		{name: "sources-only-callback", informationOnly: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, wantSources: []module.Version{a1, b1}},
		{name: "sources-rejected", fail: true, sourceMode: "reject", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantSources: []module.Version{a1}},
		{name: "sources-cancelled", fail: true, sourceMode: "cancel", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantSources: []module.Version{a1}},
		{name: "sources-changed-sums", fail: true, sourceMode: "go.sum", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantSources: []module.Version{a1}},
		{name: "information-known-go", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}, wantInformation: []module.Version{a1, b1}},
		{name: "information-legacy", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}, wantInformation: []module.Version{a1, b1, c1}},
		{name: "information-workspace", workspace: true, roots: []module.Version{a1, d1}, modules: []graphModule{node(a1, "1.17", b1), node(d1, "1.17", b2)}, want: []module.Version{a1, d1, b2}, wantInformation: []module.Version{a1, d1, b2}},
		{name: "information-workspace-upgrade", workspace: true, roots: []module.Version{a1, d1}, memberRoots: []module.Version{a3}, modules: []graphModule{node(d1, "1.17", a2), node(a3, "1.17", c1)}, want: []module.Version{a1, d1, a3, c1}, wantInformation: []module.Version{a3, d1, c1}},
		{name: "information-readonly-rejection", fail: true, roots: []module.Version{a1, b1}, modules: []graphModule{node(b1, "1.17", a2), node(a2, "1.17", z1)}, want: []module.Version{a1, b1, a2}, wantInformation: []module.Version{a2, b1}},
		{name: "information-replacement", roots: []module.Version{a1}, replacements: []string{a1.Path + " => " + c1.String(), a1.String() + " => " + d1.String()}, modules: []graphModule{node(d1, "1.17", b1)}, want: []module.Version{d1, b1}, wantInformation: []module.Version{d1, b1}},
		{name: "information-versioned-main-target", workspace: true, memberPath: b1.Path, roots: []module.Version{a1}, replacements: []string{a1.String() + " => " + b1.String()}, want: []module.Version{b1}, wantInformation: []module.Version{b1}},
		{name: "information-local", roots: []module.Version{a1}, replacements: []string{a1.String() + " => ../local"}, local: "module " + a1.Path + "\ngo 1.17\nrequire " + b1.Path + " " + b1.Version + "\n", want: []module.Version{b1}, wantInformation: []module.Version{b1}},
		{name: "information-concurrent-replacements", roots: []module.Version{a1}, replacements: []string{x1.String() + " => " + b1.String(), b1.String() + " => " + c1.String()}, modules: []graphModule{node(a1, "1.17", x1, b1)}, want: []module.Version{a1, b1}, wantInformation: []module.Version{a1, b1, c1}},
		{name: "information-shared-target", roots: []module.Version{a1}, replacements: []string{x1.String() + " => " + b1.String(), z1.String() + " => " + b1.String()}, modules: []graphModule{node(a1, "1.17", x1, z1)}, want: []module.Version{a1, b1}, wantInformation: []module.Version{a1, b1}},
		{name: "information-unrecorded-selected-manifest", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, unrecorded: []module.Version{b1}, want: []module.Version{a1}, wantInformation: []module.Version{a1, b1}},
		{name: "information-cold-frontier", roots: []module.Version{a1}, cold: []module.Version{a1}, wantInformation: []module.Version{}},
		{name: "information-unrecorded-frontier", fail: true, roots: []module.Version{a1}, unrecorded: []module.Version{a1}, wantInformation: []module.Version{}},
		{name: "information-only-callback", informationOnly: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, wantInformation: []module.Version{a1, b1}},
		{name: "information-missing", informationMode: "missing", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}, wantInformation: []module.Version{a1}},
		{name: "information-rejected", fail: true, informationMode: "reject", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantInformation: []module.Version{a1, b1}},
		{name: "information-cancelled", fail: true, informationMode: "cancel", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantInformation: []module.Version{a1, b1}},
		{name: "information-changed-sums", fail: true, informationMode: "go.sum", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, wantInformation: []module.Version{a1, b1}},
		{name: "pruned-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1}, poison: []module.Version{c1}},
		{name: "legacy-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1), node(b1, "1.16", c1)}, want: []module.Version{a1, b1}, poison: []module.Version{c1}},
		{name: "legacy-root", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}},
		{name: "no-go-root", roots: []module.Version{a1}, modules: []graphModule{node(a1, "", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}},
		{name: "unpruned-main", mainGo: "1.16", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}},
		{name: "unpruned-mvs-rejection", mainGo: "1.16", fail: true, roots: []module.Version{a1, b1}, modules: []graphModule{node(a1, "1.17", c1), node(b1, "1.17", a2), node(a2, "1.17", d1)}, want: []module.Version{a1, b1, a2, c1, d1}},
		{name: "pruned-mvs-info-after-rejection", fail: true, roots: []module.Version{a1, b1}, modules: []graphModule{node(b1, "1.17", a2), node(a2, "1.17", z1)}, want: []module.Version{a1, b1, a2}, poison: []module.Version{z1}},
		{name: "workspace-pruned-info", workspace: true, roots: []module.Version{a1, d1}, modules: []graphModule{node(a1, "1.17", b1), node(d1, "1.17", b2), node(b2, "1.17", z1)}, want: []module.Version{a1, d1, b2}, poison: []module.Version{b1, z1}},
		{name: "workspace-upgrade-barrier", workspace: true, roots: []module.Version{a1, d1}, memberRoots: []module.Version{a3}, modules: []graphModule{node(d1, "1.17", a2), node(a3, "1.17", c1)}, want: []module.Version{a1, d1, a3, c1}, poison: []module.Version{a2}},
		{name: "workspace-intermediate-upgrade", workspace: true, roots: []module.Version{a1, b1}, modules: []graphModule{node(b1, "1.17", a2), node(a2, "1.17", c1), node(c1, "1.17", a3), node(a3, "1.17", d1)}, want: []module.Version{a1, b1, a2, c1, a3, d1}, strip: []module.Version{a2}},
		{name: "workspace-legacy-member", workspace: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1), node(b1, "1.17", c1)}, want: []module.Version{a1, b1, c1}, mainGo: "1.16"},
		{name: "unknown-cold-frontier", workspace: true, roots: []module.Version{a1, d1}, modules: []graphModule{node(a1, "1.17", c2), node(d1, "1.17", c3)}, cold: []module.Version{d1}, want: []module.Version{a1}},
		{name: "unknown-sum-frontier", workspace: true, roots: []module.Version{a1, d1}, modules: []graphModule{node(a1, "1.17", c2), node(d1, "1.17", c3)}, unrecorded: []module.Version{d1}, want: []module.Version{a1}},
		{name: "missing-info-sum", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, unrecorded: []module.Version{b1}, want: []module.Version{a1}},
		{name: "cycle", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1), node(b1, "1.17", c1), node(c1, "1.17", b1)}, want: []module.Version{a1, b1, c1}},
		{name: "malformed-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, altered: map[module.Version]string{b1: "module " + b1.Path + "\nrequire (\ninvalid\n"}, want: []module.Version{a1, b1}},
		{name: "missing-module-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, altered: map[module.Version]string{b1: "go 1.17\n"}, want: []module.Version{a1, b1}},
		{name: "other-module-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, altered: map[module.Version]string{b1: "module example.com/other\ngo 1.17\n"}, want: []module.Version{a1, b1}},
		{name: "too-new-info-only", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, altered: map[module.Version]string{b1: "module " + b1.Path + "\ngo 1.99.0\n"}, want: []module.Version{a1, b1}},
		{name: "too-new-graph-frontier", fail: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1)}, altered: map[module.Version]string{b1: "module " + b1.Path + "\ngo 1.99.0\nrequire " + c1.Path + " " + c1.Version + "\n"}, want: []module.Version{a1, b1}, poison: []module.Version{c1}},
		{name: "workspace-initialization-failure", workspace: true, mainGo: "1.26.1", workGo: "1.26", fail: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1}, poison: []module.Version{b1}},
		{name: "known-replacement-go-version", roots: []module.Version{x1, a1}, replacements: []string{x1.String() + " => " + b1.String(), b1.String() + " => " + c1.String()}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}, poison: []module.Version{c1}},
		{name: "concurrent-replacement-info", roots: []module.Version{a1}, replacements: []string{x1.String() + " => " + b1.String(), b1.String() + " => " + c1.String()}, modules: []graphModule{node(a1, "1.17", x1, b1)}, want: []module.Version{a1, b1}, altered: map[module.Version]string{c1: "invalid info-only manifest\n"}},
		{name: "local-replacement-transitive", roots: []module.Version{a1}, replacements: []string{a1.String() + " => ../local"}, local: "module " + a1.Path + "\ngo 1.17\nrequire " + b1.Path + " " + b1.Version + "\n", want: []module.Version{b1}},
		{name: "workspace-local-replacement-transitive", workspace: true, roots: []module.Version{a1}, replacements: []string{a1.String() + " => ../local"}, local: "module " + a1.Path + "\ngo 1.17\nrequire " + b1.Path + " " + b1.Version + "\n", want: []module.Version{b1}},
		{name: "cached-summary-info-only", roots: []module.Version{x1, a1}, replacements: []string{x1.String() + " => " + b1.String()}, modules: []graphModule{node(a1, "1.17", b1)}, altered: map[module.Version]string{b1: "module " + x1.Path + "\n"}, want: []module.Version{a1, b1}},
		{name: "transitive-exclusion", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1)}, exclusions: []module.Version{b1}, want: []module.Version{a1}, poison: []module.Version{b1}},
		{name: "transitive-replacement", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, replacements: []string{b1.Path + " => " + c1.String(), b1.String() + " => " + d1.String()}, want: []module.Version{a1, d1}, poison: []module.Version{b1, c1}},
		{name: "workspace-transitive-replacement", workspace: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, replacements: []string{b1.Path + " => " + c1.String(), b1.String() + " => " + d1.String()}, want: []module.Version{a1, d1}, poison: []module.Version{b1, c1}},
		{name: "transitive-callback-rejection", fail: true, callback: "reject", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}},
		{name: "transitive-callback-manifest-drift", fail: true, callback: "go.mod", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}},
		{name: "transitive-callback-checksum-drift", fail: true, callback: "go.sum", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, want: []module.Version{a1, b1}},
		{name: "pruned-info-batch", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1, c1, d1)}, want: []module.Version{a1, b1, c1, d1}, batches: []int{1, 3}},
		{name: "unpruned-graph-batch", roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.16", b1, c1, d1)}, want: []module.Version{a1, b1, c1, d1}, batches: []int{1, 3}},
		{name: "workspace-original-version-query", workspace: true, rawRequirement: true, direct: true, roots: []module.Version{a1}, modules: []graphModule{node(a1, "1.17", b1)}, poison: []module.Version{b1}},
		{name: "workspace-original-replacement-query", workspace: true, direct: true, roots: []module.Version{a1}, replacements: []string{b1.Path + " v1 => " + c1.String()}, memberReplacements: []string{b1.String() + " => " + d1.String()}},
		{name: "workspace-main-replacement-ignored", workspace: true, replacements: []string{"example.com/member => " + d1.String()}, poison: []module.Version{d1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			modules := map[module.Version]graphModule{}
			for _, mod := range []module.Version{a1, a2, a3, b1, b2, c1, c2, c3, d1, z1, x1, unused} {
				modules[mod] = node(mod, "1.17")
			}
			for _, mod := range test.modules {
				modules[mod.mod] = mod
			}
			var proxy string
			var versions []module.Version
			for mod, declaration := range modules {
				source := "module " + mod.Path + "\n"
				if declaration.goLine != "" {
					source += "go " + declaration.goLine + "\n"
				}
				for _, required := range declaration.requires {
					source += "require " + required.Path + " " + required.Version + "\n"
				}
				proxy = writeModuleProxy(t, root, mod.Path, mod.Version, source)
				versions = append(versions, mod)
			}
			module.Sort(versions)
			writeFile(t, filepath.Join(root, "proxy", filepath.FromSlash(a1.Path), "@v", "list"), "v1.0.0\nv1.1.0\nv1.2.0\n")
			writeFile(t, filepath.Join(root, "proxy", filepath.FromSlash(b1.Path), "@v", "list"), "v1.0.0\nv1.1.0\n")
			app := filepath.Join(root, "app")
			writeModule(t, app, "example.com/app")
			writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
			env := isolatedGoEnvironment(t, proxy)
			args := []string{"mod", "download", "-json"}
			for _, mod := range versions {
				args = append(args, mod.String())
			}
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, args...)
			if err != nil {
				t.Fatal(err)
			}
			type download struct{ Path, Version, Sum, GoModSum, GoMod, Dir string }
			facts := map[module.Version]download{}
			decoder := json.NewDecoder(strings.NewReader(string(output)))
			var sums strings.Builder
			for {
				var downloaded download
				if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
					break
				} else if err != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
					t.Fatalf("invalid fixture checksum facts: %v", err)
				}
				mod := module.Version{Path: downloaded.Path, Version: downloaded.Version}
				if source, exists := test.altered[mod]; exists {
					writeFile(t, downloaded.GoMod, source)
					downloaded.GoModSum, err = dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
						return io.NopCloser(strings.NewReader(source)), nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				facts[mod] = downloaded
				sums.WriteString(downloaded.Path + " " + downloaded.Version + " " + downloaded.Sum + "\n")
				if !slices.Contains(test.unrecorded, mod) {
					sums.WriteString(downloaded.Path + " " + downloaded.Version + "/go.mod " + downloaded.GoModSum + "\n")
				}
			}
			mainGo := test.mainGo
			if mainGo == "" {
				mainGo = "1.26"
			}
			manifest := func(path string, requires []module.Version) string {
				source := "module " + path + "\ngo " + mainGo + "\n"
				for _, required := range requires {
					source += "require " + required.Path + " " + required.Version + "\n"
				}
				return source
			}
			mainManifest := manifest("example.com/app", test.roots)
			if test.rawRequirement {
				mainManifest = strings.Replace(mainManifest, a1.Path+" "+a1.Version, a1.Path+" v1", 1)
			}
			for _, replacement := range test.replacements {
				mainManifest += "replace " + strings.ReplaceAll(replacement, "@", " ") + "\n"
			}
			for _, exclusion := range test.exclusions {
				mainManifest += "exclude " + exclusion.Path + " " + exclusion.Version + "\n"
			}
			writeFile(t, filepath.Join(app, "go.mod"), mainManifest)
			writeFile(t, filepath.Join(app, "go.sum"), sums.String())
			if test.local != "" {
				writeFile(t, filepath.Join(root, "local", "go.mod"), test.local)
			}
			if test.workspace {
				memberPath := test.memberPath
				if memberPath == "" {
					memberPath = "example.com/member"
				}
				memberManifest := manifest(memberPath, test.memberRoots)
				for _, replacement := range test.memberReplacements {
					memberManifest += "replace " + strings.ReplaceAll(replacement, "@", " ") + "\n"
				}
				writeFile(t, filepath.Join(root, "member", "go.mod"), memberManifest)
				work := filepath.Join(root, "go.work")
				workGo := test.workGo
				if workGo == "" {
					workGo = "1.26"
				}
				writeFile(t, work, "go "+workGo+"\nuse (\n./app\n./member\n)\n")
				env = append(env, "GOWORK="+work)
			}
			for _, mod := range append(test.poison, unused) {
				writeFile(t, facts[mod].GoMod, "invalid unrelated manifest\n")
			}
			for _, mod := range test.cold {
				if err := os.Remove(facts[mod].GoMod); err != nil {
					t.Fatal(err)
				}
			}
			for _, mod := range test.strip {
				if err := os.RemoveAll(facts[mod].Dir); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(strings.TrimSuffix(facts[mod].GoMod, ".mod") + ".zip"); err != nil {
					t.Fatal(err)
				}
			}
			if test.informationMode == "missing" {
				if err := os.Remove(strings.TrimSuffix(facts[b1].GoMod, ".mod") + ".info"); err != nil {
					t.Fatal(err)
				}
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			realGo, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(root, "commands")
			start := stage.Root()
			if !test.direct {
				env = stage.Environment()
			} else {
				start = app
			}
			env = append(env, "PLYSTRA_MODULE_DEPENDENCY_HELPER=root-manifests", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_COMMAND_TRACE="+trace)
			var retained []string
			var batches []int
			rejected := errors.New("transitive manifest rejected")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := moduledependency.Options{Environment: env, GoCommand: os.Args[0], RetainRootManifests: func(_ context.Context, manifests []moduledependency.RootManifest) error {
				batches = append(batches, len(manifests))
				commands, _ := os.ReadFile(trace)
				if strings.Contains(string(commands), "list ") || strings.Contains(string(commands), "download ") {
					t.Fatalf("graph capture followed Go consumption: %s", commands)
				}
				var metadata []projectstage.MetadataInput
				for _, input := range manifests {
					retained = append(retained, input.Path)
					metadata = append(metadata, projectstage.MetadataInput{Path: input.Path, GoModChecksum: input.Checksum})
				}
				if err := stage.RetainMetadata(t.Context(), metadata); err != nil {
					return err
				}
				if slices.Contains(retained, facts[b1].GoMod) && test.callback != "" {
					if test.callback == "reject" {
						return rejected
					}
					path := filepath.Join(stage.Root(), test.callback)
					writeFile(t, path, string(readFile(t, path))+"\n")
				}
				return nil
			}}
			var information []string
			informationCalls := 0
			if test.informationOnly {
				options.RetainRootManifests = nil
			}
			if test.wantInformation != nil {
				options.RetainModuleInformation = func(_ context.Context, paths []string) error {
					commands, _ := os.ReadFile(trace)
					early := !strings.Contains(string(commands), "list ") && !strings.Contains(string(commands), "download ")
					if early {
						informationCalls++
					}
					var metadata []projectstage.MetadataInput
					for _, path := range paths {
						if early {
							information = append(information, path)
						}
						metadata = append(metadata, projectstage.MetadataInput{Path: path})
					}
					if err := stage.RetainMetadata(ctx, metadata); err != nil {
						return err
					}
					switch test.informationMode {
					case "reject":
						return rejected
					case "cancel":
						cancel()
					case "go.sum":
						path := filepath.Join(stage.Root(), "go.sum")
						writeFile(t, path, string(readFile(t, path))+"\n")
						return rejected
					}
					return nil
				}
			}
			var sources []module.Version
			if test.wantSources != nil {
				options.RetainSelectedSources = func(group []moduledependency.Module) error {
					commands := string(readFile(t, trace))
					early := !strings.Contains(commands, "list ") && !strings.Contains(commands, "download ")
					var metadata []projectstage.MetadataInput
					var trees []projectstage.Source
					for _, source := range group {
						version := module.Version{Path: source.Path(), Version: source.SelectedVersion()}
						if replacement, ok := source.Replacement(); ok {
							version = module.Version{Path: replacement.Path(), Version: replacement.Version()}
						}
						if source.Workspace() || source.Checksum() != facts[version].Sum || source.GoModChecksum() != facts[version].GoModSum || source.GoModFile() != facts[version].GoMod || source.SourceDirectory() != facts[version].Dir || len(source.ProjectGoMod()) != 0 {
							t.Fatal("source capture lost effective provenance or parsed its manifest")
						}
						if early && len(trees) == 0 {
							if slices.Contains(sources, version) {
								t.Fatal("shared effective source captured more than once")
							}
							sources = append(sources, version)
						}
						metadata = append(metadata, projectstage.MetadataInput{Path: source.GoModFile(), GoModChecksum: source.GoModChecksum()}, projectstage.MetadataInput{
							Path: strings.TrimSuffix(source.GoModFile(), ".mod") + ".ziphash", Expected: []byte(source.Checksum()),
						})
						trees = append(trees, projectstage.Source{Directory: source.SourceDirectory(), Module: version, Checksum: source.Checksum()})
					}
					if err := stage.RetainMetadata(ctx, metadata); err != nil {
						return err
					}
					if err := stage.RetainSources(ctx, trees); err != nil {
						return err
					}
					switch test.sourceMode {
					case "reject":
						return rejected
					case "cancel":
						cancel()
					case "go.sum":
						path := filepath.Join(stage.Root(), "go.sum")
						writeFile(t, path, string(readFile(t, path))+"\n")
						return rejected
					}
					return nil
				}
			}
			index, err := moduledependency.Discover(ctx, locate(t, start), options)
			if (err != nil) != test.fail {
				t.Fatalf("unexpected Go listing result: %v", err)
			}
			if test.batches != nil && !slices.Equal(batches, test.batches) {
				t.Fatalf("retention split a demanded frontier: %v, want %v", batches, test.batches)
			}
			if test.rawRequirement {
				selected, ok := index.ByPath(a1.Path)
				if !ok || selected.SelectedVersion() != a3.Version {
					t.Fatalf("Go version query did not select the latest prefix match: %v", index.Modules())
				}
			}
			if test.callback != "" {
				wantError := rejected
				if test.callback != "reject" {
					wantError = moduledependency.ErrConcurrentChange
				}
				if !errors.Is(err, wantError) || strings.Contains(string(readFile(t, trace)), "list ") {
					t.Fatalf("failed transitive capture reached Go listing: %v", err)
				}
			}
			if test.informationMode == "reject" || test.informationMode == "cancel" || test.informationMode == "go.sum" {
				wantError := rejected
				if test.informationMode == "cancel" {
					wantError = context.Canceled
				}
				if !errors.Is(err, wantError) || test.informationMode == "go.sum" && !errors.Is(err, moduledependency.ErrConcurrentChange) || strings.Contains(string(readFile(t, trace)), "list ") {
					t.Fatalf("failed information capture reached Go: %v", err)
				}
			}
			if test.sourceMode != "" {
				wantError := rejected
				if test.sourceMode == "cancel" {
					wantError = context.Canceled
				}
				if !errors.Is(err, wantError) || test.sourceMode == "go.sum" && !errors.Is(err, moduledependency.ErrConcurrentChange) || strings.Contains(string(readFile(t, trace)), "list ") {
					t.Fatalf("failed source capture reached Go: %v", err)
				}
			}
			module.Sort(sources)
			module.Sort(test.wantSources)
			if !slices.Equal(sources, test.wantSources) {
				t.Fatalf("wrong selected source set: got %v, want %v", sources, test.wantSources)
			}
			var wantInformation []string
			for _, mod := range test.wantInformation {
				wantInformation = append(wantInformation, strings.TrimSuffix(facts[mod].GoMod, ".mod")+".info")
			}
			sort.Strings(wantInformation)
			sort.Strings(information)
			if !slices.Equal(information, wantInformation) || len(information) != 0 && informationCalls != 1 {
				t.Fatalf("wrong selected information batch: got %v, want %v, callbacks=%d", information, wantInformation, informationCalls)
			}
			var want []string
			for _, mod := range test.want {
				want = append(want, facts[mod].GoMod)
			}
			sort.Strings(want)
			sort.Strings(retained)
			if !reflect.DeepEqual(retained, want) {
				t.Fatalf("wrong consumed manifest set:\nretained %v\nwant %v", retained, want)
			}
			for _, mod := range test.strip {
				if _, err := os.Stat(strings.TrimSuffix(facts[mod].GoMod, ".mod") + ".zip"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("capture prefetched an intermediate source archive")
				}
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			writeFile(t, facts[unused].GoMod, "another unrelated manifest\n")
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("unrelated manifest invalidated retained graph evidence")
			}
			if test.wantSources != nil {
				writeFile(t, filepath.Join(facts[unused].Dir, "unrelated.go"), "package unrelated\n")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unselected source invalidated retained evidence")
				}
				if len(sources) != 0 {
					path := filepath.Join(facts[sources[0]].Dir, "go.mod")
					data := readFile(t, path)
					info, err := os.Stat(path)
					if err != nil || !os.SameFile(info, info) {
						t.Fatal("cannot initialize source identity")
					}
					if err := os.Rename(path, path+".prior"); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, string(data))
					if err := os.Chmod(path, info.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
					replacement, err := os.Stat(path)
					if err != nil || os.SameFile(info, replacement) {
						t.Fatal("source identity fixture failed")
					}
					if err := os.Remove(path + ".prior"); err != nil {
						t.Fatal(err)
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("source identity survived cleanup: %v", err)
					}
				}
				return
			}
			if test.wantInformation != nil {
				writeFile(t, strings.TrimSuffix(facts[unused].GoMod, ".mod")+".info", "unrelated information")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unselected information invalidated retained evidence")
				}
				if len(information) != 0 {
					path := information[0]
					data := readFile(t, path)
					info, err := os.Stat(path)
					if err != nil || !os.SameFile(info, info) {
						t.Fatal("cannot initialize information identity")
					}
					if err := os.Rename(path, path+".prior"); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, string(data))
					if err := os.Chmod(path, info.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
					replacement, err := os.Stat(path)
					if err != nil || os.SameFile(info, replacement) {
						t.Fatal("information identity fixture failed")
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("information identity survived cleanup: %v", err)
					}
				}
				return
			}
			for _, mod := range test.want {
				if slices.Contains(test.roots, mod) {
					continue
				}
				path := facts[mod].GoMod
				data := readFile(t, path)
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(info, info) {
					t.Fatal("cannot retain manifest identity")
				}
				if err := os.Rename(path, path+".prior"); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(data))
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("%s identity replacement survived cleanup: %v", mod, err)
				}
				break
			}
		})
	}
}
