package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"golang.org/x/mod/module"
)

func TestWorkspaceGraphReadsHistoricalMainModuleManifest(t *testing.T) {
	root := t.TempDir()
	app, member := filepath.Join(root, "app"), filepath.Join(root, "member")
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire example.com/member v1.0.0\n")
	writeModule(t, member, "example.com/member")
	work := filepath.Join(root, "go.work")
	writeFile(t, work, "go 1.26\nuse (\n./app\n./member\n)\n")
	env := append(isolatedGoEnvironment(t, filepath.Join(root, "proxy")), "GOWORK="+work)
	cache := environmentValue(t, env, "GOMODCACHE")
	writeFile(t, filepath.Join(cache, "cache", "download", "example.com", "member", "@v", "v1.0.0.mod"), "invalid historical manifest\n")
	_, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, "list", "-m", "-json", "-mod=readonly", "all")
	if err == nil || !strings.Contains(err.Error(), "parsing go.mod: missing module line") {
		t.Fatalf("historical workspace manifest was not consumed: %v", err)
	}
}

func TestDiscoverRetainsEffectiveWorkspaceRootManifests(t *testing.T) {
	const path, first, second = "example.com/original", "example.com/first", "example.com/second"
	exact := func(target string) string { return "replace " + path + " v1.0.0 => " + target + " v1.0.0\n" }
	wildcard := func(target string) string { return "replace " + path + " => " + target + " v1.0.0\n" }
	unmatched := "replace " + path + " v1.9.0 => " + first + " v1.0.0\n"
	for _, test := range []struct {
		name, app, member, work, requirement string
		want                                 []string
		fail, reverse, main                  bool
	}{
		{name: "raw-roots", want: []string{path + "@v1.0.0"}},
		{name: "member-root", member: "require " + second + " v1.0.0\n", want: []string{path + "@v1.0.0", second + "@v1.0.0"}},
		{name: "duplicate-root", member: "require " + path + " v1.0.0\n", want: []string{path + "@v1.0.0"}},
		{name: "multiple-versions", member: "require " + path + " v1.1.0\n", want: []string{path + "@v1.0.0", path + "@v1.1.0"}},
		{name: "main-module-history", main: true, want: []string{path + "@v1.0.0"}},
		{name: "member-replacement", member: exact(first), want: []string{first + "@v1.0.0"}},
		{name: "member-exact", app: exact(first) + wildcard(second), want: []string{first + "@v1.0.0"}},
		{name: "member-exact-reversed", app: wildcard(second) + exact(first), want: []string{first + "@v1.0.0"}},
		{name: "work-wildcard", app: exact(first), work: wildcard(second), want: []string{second + "@v1.0.0"}},
		{name: "work-exact", work: wildcard(second) + exact(first), want: []string{first + "@v1.0.0"}},
		{name: "work-exact-reversed", work: exact(first) + wildcard(second), want: []string{first + "@v1.0.0"}},
		{name: "work-nonmatching", member: wildcard(second), work: unmatched, want: []string{second + "@v1.0.0"}},
		{name: "cross-member-wildcard-last", app: exact(first), member: wildcard(second), want: []string{second + "@v1.0.0"}},
		{name: "cross-member-exact-last", app: exact(first), member: wildcard(second), reverse: true, fail: true},
		{name: "cross-member-same-target", app: wildcard(first), member: exact(first), want: []string{first + "@v1.0.0"}},
		{name: "conflicting-exact", app: exact(first), member: exact(second), fail: true},
		{name: "conflicting-wildcard", app: wildcard(first), member: wildcard(second), fail: true},
		{name: "overridden-path-conflict", app: wildcard(first), member: wildcard(second), work: unmatched, want: []string{second + "@v1.0.0"}},
		{name: "overridden-path-conflict-reversed", app: wildcard(first), member: wildcard(second), work: unmatched, reverse: true, want: []string{first + "@v1.0.0"}},
		{name: "overridden-path-exact-conflict", app: exact(first), member: exact(second), work: unmatched, fail: true},
		{name: "local-member", member: "replace " + path + " => ../local\n"},
		{name: "local-work", app: exact(first), work: "replace " + path + " => ./local\n"},
		{name: "equal-local-wildcard", app: "replace " + path + " => ../local\n", member: "replace " + path + " => ../app/../local\n"},
		{name: "equal-local-exact", app: "replace " + path + " v1.0.0 => ../local\n", member: "replace " + path + " v1.0.0 => ../app/../local\n", fail: true},
		{name: "different-local-bases", app: "replace " + path + " => ./local\n", member: "replace " + path + " => ./local\n", fail: true},
		{name: "overridden-local-bases", app: "replace " + path + " v1.0.0 => ./local\n", member: "replace " + path + " v1.0.0 => ./local\n", work: unmatched},
		{name: "exclude-original", app: "exclude " + path + " v1.0.0\n", member: "require " + path + " v1.0.0\n", requirement: second + " v1.0.0", want: []string{second + "@v1.0.0"}},
		{name: "exclude-effective", app: exact(first), member: "exclude " + first + " v1.0.0\n", want: []string{first + "@v1.0.0"}},
		{name: "conflicting-member-duplicates", app: exact(first) + exact(second), work: wildcard(second), fail: true},
		{name: "conflicting-work-duplicates", work: wildcard(first) + wildcard(second), fail: true},
		{name: "replaced-main-wildcard", main: true, work: wildcard(first), fail: true},
		{name: "replaced-main-version", main: true, work: exact(first), want: []string{first + "@v1.0.0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, "v1.0.0")
			writeModuleProxy(t, root, path, "v1.1.0")
			writeModuleProxy(t, root, first, "v1.0.0")
			writeModuleProxy(t, root, second, "v1.0.0")
			app, member := filepath.Join(root, "app"), filepath.Join(root, "member")
			writeModule(t, app, "example.com/app")
			for _, directory := range []string{filepath.Join(root, "local"), filepath.Join(app, "local"), filepath.Join(member, "local")} {
				writeModule(t, directory, path)
			}
			env := isolatedGoEnvironment(t, proxy)
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, "mod", "download", "-json", path+"@v1.0.0", path+"@v1.1.0", first+"@v1.0.0", second+"@v1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			expected := map[string]moduledependency.RootManifest{}
			var sums strings.Builder
			decoder := json.NewDecoder(strings.NewReader(string(output)))
			for {
				var downloaded struct{ Path, Version, Sum, GoModSum, GoMod string }
				if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
					break
				} else if err != nil || downloaded.GoModSum == "" || downloaded.Sum == "" {
					t.Fatalf("invalid fixture checksum evidence: %v", err)
				}
				expected[downloaded.Path+"@"+downloaded.Version] = moduledependency.RootManifest{Path: downloaded.GoMod, Checksum: downloaded.GoModSum}
				sums.WriteString(downloaded.Path + " " + downloaded.Version + " " + downloaded.Sum + "\n" + downloaded.Path + " " + downloaded.Version + "/go.mod " + downloaded.GoModSum + "\n")
			}
			requirement := path + " v1.0.0"
			if test.requirement != "" {
				requirement = test.requirement
			}
			writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire "+requirement+"\n"+test.app)
			memberPath := "example.com/member"
			if test.main {
				memberPath = path
			}
			writeFile(t, filepath.Join(member, "go.mod"), "module "+memberPath+"\ngo 1.26\n"+test.member)
			writeFile(t, filepath.Join(app, "go.sum"), sums.String())
			use := "./app\n./member\n"
			if test.reverse {
				use = "./member\n./app\n"
			}
			work := filepath.Join(root, "go.work")
			writeFile(t, work, "go 1.26\nuse (\n"+use+")\n"+test.work)
			env = append(env, "GOWORK="+work)
			if test.fail {
				if _, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, "list", "-m", "-json", "-mod=readonly", "all"); err == nil {
					t.Fatal("ordinary Go accepted the supposed replacement conflict")
				}
			}
			realGo, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(root, "commands")
			env = append(env, "PLYSTRA_MODULE_DEPENDENCY_HELPER=root-manifests", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_COMMAND_TRACE="+trace)
			called := false
			index, err := moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{Environment: env, GoCommand: os.Args[0], RetainRootManifests: func(_ context.Context, manifests []moduledependency.RootManifest) error {
				called = true
				if len(manifests) != len(test.want) {
					t.Fatalf("wrong root capture: %v, want %v", manifests, test.want)
				}
				for _, key := range test.want {
					found := false
					for _, manifest := range manifests {
						found = found || manifest == expected[key]
					}
					if !found {
						t.Fatalf("root %s not retained: %v", key, manifests)
					}
				}
				if commands := string(readFile(t, trace)); strings.Contains(commands, "list ") || strings.Contains(commands, "download ") {
					t.Fatalf("workspace capture happened after resolution: %s", commands)
				}
				return nil
			}})
			if (err != nil) != test.fail || called != (len(test.want) != 0) {
				t.Fatalf("capture=%v, error=%v; want roots=%v, error=%v", called, err, test.want, test.fail)
			}
			if err == nil && !test.main && len(test.want) == 1 {
				requiredPath, _, _ := strings.Cut(requirement, " ")
				dependency, ok := index.ByPath(requiredPath)
				selected := dependency.Path() + "@" + dependency.SelectedVersion()
				if replacement, replaced := dependency.Replacement(); replaced {
					selected = replacement.Path() + "@" + replacement.Version()
				}
				if !ok || selected != test.want[0] {
					t.Fatalf("captured replacement disagrees with Go selection: %s, want %s", selected, test.want[0])
				}
			}
		})
	}
}

func TestDiscoverRetainsWorkspaceRootManifestChecksumAuthority(t *testing.T) {
	for _, mode := range []string{
		"member", "member-match", "member-duplicates", "member-conflict", "member-conflict-first", "member-conflict-last",
		"primary-match", "primary-match-first", "primary-conflict-first", "primary-unknown-empty", "member-empty", "unknown", "invalid-primary-first", "invalid-primary-last", "invalid-member", "malformed-member",
		"callback-error", "change-project", "change-project-sum", "change-member", "change-member-sum", "change-workspace", "change-workspace-sum",
		"list-project-identity", "list-project-sum-identity", "list-member-identity", "list-member-sum-identity", "list-workspace-identity", "list-workspace-sum-identity",
		"list-project-sum-create", "list-member-sum-create", "list-workspace-sum-create",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const path, version = "example.com/Source/Dependency", "v1.2.3"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, version)
			app, member := filepath.Join(root, "app"), filepath.Join(root, "member")
			writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire "+path+" "+version+"\n")
			writeModule(t, member, "example.com/member")
			work := filepath.Join(root, "go.work")
			writeFile(t, work, "go 1.26\nuse (\n./app\n./member\n)\n")
			env := isolatedGoEnvironment(t, proxy)
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, "mod", "download", "-json", path+"@"+version)
			var downloaded struct{ Sum, GoModSum string }
			if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
				t.Fatalf("seed checksum authority: %v", err)
			}
			row := func(hash string) string { return path + " " + version + "/go.mod " + hash + "\n" }
			good := row(downloaded.GoModSum)
			badHash := "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
			bad, empty, unknown := row(badHash), row("h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY="), row("other:ignored")
			archive := path + " " + version + " " + downloaded.Sum + "\n"
			projectSums, memberSums, primary := archive, good, ""
			wantChecksum, wantError := downloaded.GoModSum, false
			switch mode {
			case "list-member-sum-create":
				projectSums += good
			case "member-match":
				projectSums += good
			case "member-duplicates":
				memberSums += good
			case "member-conflict":
				projectSums += bad
				wantChecksum, wantError = "", true
			case "member-conflict-first":
				memberSums, wantChecksum, wantError = bad+good, "", true
			case "member-conflict-last":
				memberSums, wantChecksum, wantError = good+bad, "", true
			case "primary-match":
				primary, memberSums = good, bad
			case "primary-match-first":
				primary, memberSums = good+bad, bad
			case "primary-conflict-first":
				primary, wantChecksum, wantError = bad+good, badHash, true
			case "primary-unknown-empty":
				primary, memberSums = unknown+empty+good, bad
			case "member-empty":
				memberSums = empty + good
			case "unknown":
				primary, memberSums, wantChecksum = empty+unknown, "", ""
			case "invalid-primary-first":
				primary, wantChecksum, wantError = row("h1:garbage")+good, "", true
			case "invalid-primary-last":
				primary, memberSums = good+row("h1:garbage"), bad
			case "invalid-member":
				memberSums, wantChecksum, wantError = row("h1:garbage")+good, "", true
			case "malformed-member":
				primary, memberSums, wantChecksum, wantError = good, "unrelated malformed\n", "", true
			case "callback-error":
				wantError = true
			}
			paths := map[string]string{
				"project": filepath.Join(app, "go.mod"), "project-sum": filepath.Join(app, "go.sum"),
				"member": filepath.Join(member, "go.mod"), "member-sum": filepath.Join(member, "go.sum"),
				"workspace": work, "workspace-sum": work + ".sum",
			}
			writeFile(t, paths["project-sum"], projectSums)
			writeFile(t, paths["member-sum"], memberSums)
			writeFile(t, paths["workspace-sum"], primary)
			listedName, mutation := "", ""
			if strings.HasPrefix(mode, "list-") {
				mutation = "identity"
				if strings.HasSuffix(mode, "-create") {
					mutation = "create"
				}
				listedName = strings.TrimSuffix(strings.TrimPrefix(mode, "list-"), "-"+mutation)
				wantError = true
				if mutation == "create" {
					if err := os.Remove(paths[listedName]); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := map[string]string{}
			for name, path := range paths {
				if name == listedName && mutation == "create" {
					continue
				}
				before[name] = string(readFile(t, path))
			}
			realGo, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(root, "commands")
			env = append(env, "GOWORK="+work, "PLYSTRA_MODULE_DEPENDENCY_HELPER=root-manifests", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_COMMAND_TRACE="+trace)
			marker := filepath.Join(root, "metadata-changed")
			if listedName != "" {
				env = append(env, "PLYSTRA_MODULE_METADATA_PATH="+paths[listedName], "PLYSTRA_MODULE_METADATA_MODE="+mutation, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
			}
			cache := environmentValue(t, env, "GOMODCACHE")
			escaped, _ := module.EscapePath(path)
			manifestPath := filepath.Join(cache, "cache", "download", filepath.FromSlash(escaped), "@v", version+".mod")
			rejected, called := errors.New("root capture rejected"), false
			_, err = moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{Environment: env, GoCommand: os.Args[0], RetainRootManifests: func(_ context.Context, manifests []moduledependency.RootManifest) error {
				called = true
				if wantChecksum == "" || len(manifests) != 1 || manifests[0].Path != manifestPath || manifests[0].Checksum != wantChecksum {
					t.Fatalf("wrong workspace checksum authority: %v", manifests)
				}
				commands := string(readFile(t, trace))
				if strings.Contains(commands, "list ") || strings.Contains(commands, "download ") {
					t.Fatalf("workspace capture followed resolution: %s", commands)
				}
				if mode == "callback-error" {
					return rejected
				}
				if name, changed := strings.CutPrefix(mode, "change-"); changed {
					writeFile(t, paths[name], before[name]+"\n")
				}
				return nil
			}})
			if name, changed := strings.CutPrefix(mode, "change-"); changed {
				wantError = true
				if !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("changed %s was accepted: %v", name, err)
				}
			} else if mode == "callback-error" && !errors.Is(err, rejected) {
				t.Fatalf("callback rejection lost: %v", err)
			}
			if (err != nil) != wantError || called != (wantChecksum != "") {
				t.Fatalf("workspace capture=%v, error=%v; want checksum=%q, error=%v", called, err, wantChecksum, wantError)
			}
			if listedName != "" {
				if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != "metadata changed after module list\n" || !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("first-list metadata change was not detected: marker=%v, discovery=%v", markerErr, err)
				}
			}
			if strings.HasPrefix(mode, "change-") || mode == "callback-error" || mode == "malformed-member" {
				commands, _ := os.ReadFile(trace)
				if strings.Contains(string(commands), "list ") {
					t.Fatalf("listing followed rejected inputs: %s", commands)
				}
			}
			for name, path := range paths {
				if name != listedName && mode != "change-"+name && string(readFile(t, path)) != before[name] {
					t.Fatalf("discovery rewrote %s", name)
				}
			}
		})
	}
}
