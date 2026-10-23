package projectstage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectstage"
)

func TestCreateCopiesWorkingTreeAndDetectsDrift(t *testing.T) {
	root, parent := fixture(t)
	write(t, root, "assets/message.txt", "PRIVATE_ASSET")
	write(t, root, "untracked.go", "package app\n")
	write(t, root, "generated/proto/wire-map.json", "PRIVATE_HISTORY")
	write(t, root, "dist/runtime-baseline.json", "PRIVATE_BASELINE")
	write(t, root, ".git/config", "PRIVATE_GIT")
	before := tree(t, root)
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	for name, expected := range before {
		if strings.HasPrefix(name, ".git/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(stage.Root(), filepath.FromSlash(name)))
		if err != nil || string(data) != expected {
			t.Fatalf("copy %s = %q, %v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stage.Root(), ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("copied VCS metadata: %v", err)
	}
	file, err := os.Open(filepath.Join(stage.Root(), "dist", "runtime-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Check(file); err != nil {
		file.Close()
		t.Fatalf("copy is not owner-private: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	write(t, stage.Root(), "assets/message.txt", "STAGED_CHANGE")
	if !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("staging mutated the original working tree")
	}
	write(t, root, "assets/message.txt", "CONCURRENT_CHANGE")
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("changed asset was accepted: %v", err)
	}
	for _, rendered := range []string{fmt.Sprint(stage), fmt.Sprintf("%#v", stage), fmt.Sprintf("%+v", stage)} {
		if strings.Contains(rendered, root) || strings.Contains(rendered, "PRIVATE_") || strings.Contains(rendered, stage.Root()) {
			t.Fatalf("private staging evidence leaked: %s", rendered)
		}
	}
	stagedRoot := stage.Root()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stagedRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage remains after cleanup: %v", err)
	}
	if err := stage.Close(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func TestCreateRelocatesLocalReplacementAndWorkspace(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(fmt.Sprint(workspace), func(t *testing.T) {
			root, parent := fixture(t)
			dependency := filepath.Join(filepath.Dir(root), "dependency")
			write(t, dependency, "go.mod", "module example.com/dependency\n\ngo 1.26\n")
			write(t, dependency, "value.go", "package dependency\nconst Value = 1\n")
			write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n")
			write(t, root, "app.go", "package app\nimport \"example.com/dependency\"\nvar Value = dependency.Value\n")
			env := environment("GOWORK=off")
			if workspace {
				work := filepath.Join(filepath.Dir(root), "go.work")
				write(t, filepath.Dir(work), "go.work", "go 1.26\nuse (\n ./app\n ./dependency\n)\n")
				write(t, filepath.Dir(work), "go.work.sum", "")
				env = environment("GOWORK=" + work)
			}
			beforeRoot, beforeDependency := tree(t, root), tree(t, dependency)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "list", "-mod=readonly", "-f", "{{.Dir}}", "example.com/dependency")
			if err != nil {
				t.Fatal(err)
			}
			stagedDependency := strings.TrimSpace(string(output))
			if stagedDependency == dependency || !strings.HasPrefix(stagedDependency, parent+string(filepath.Separator)) {
				t.Fatalf("dependency escaped stage: %q", stagedDependency)
			}
			write(t, stagedDependency, "value.go", "package dependency\nconst Value = 2\n")
			if !reflect.DeepEqual(beforeRoot, tree(t, root)) || !reflect.DeepEqual(beforeDependency, tree(t, dependency)) {
				t.Fatal("relocation changed original module files")
			}
			if err := stage.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			write(t, dependency, "value.go", "package dependency\nconst Value = 3\n")
			if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("dependency source drift was accepted: %v", err)
			}
		})
	}
}

func TestRecheckDetectsMembershipAndIdentityChanges(t *testing.T) {
	for _, change := range []string{"addition", "removal", "replacement", "directory"} {
		t.Run(change, func(t *testing.T) {
			root, parent := fixture(t)
			write(t, root, "assets/value", "same bytes")
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")})
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			switch change {
			case "addition":
				write(t, root, "assets/new", "new")
			case "removal":
				if err := os.Remove(filepath.Join(root, "assets", "value")); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Rename(filepath.Join(root, "assets", "value"), filepath.Join(root, "old")); err != nil {
					t.Fatal(err)
				}
				write(t, root, "assets/value", "same bytes")
			case "directory":
				if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s was accepted: %v", change, err)
			}
		})
	}
}

func TestCreateRejectsUnsafeInputsAndCleansOwnedOutput(t *testing.T) {
	for _, name := range []string{"nested-temporary-parent", "alternate-modfile", "overlay", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			root, parent := fixture(t)
			options := projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")}
			ctx := t.Context()
			switch name {
			case "nested-temporary-parent":
				options.TemporaryParent = root
			case "alternate-modfile":
				options.Environment = append(options.Environment, "GOFLAGS=-modfile=private.mod")
			case "overlay":
				options.Environment = append(options.Environment, "GOFLAGS=-overlay=private.json")
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			before := tree(t, root)
			if stage, err := projectstage.Create(ctx, options); err == nil {
				stage.Close()
				t.Fatal("unsafe staging was accepted")
			}
			if !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("failed staging changed original files")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed staging left output: %v, %v", entries, err)
			}
		})
	}
}

func TestCreateWorkspaceReplacementOverridesMainModule(t *testing.T) {
	root, parent := fixture(t)
	base := filepath.Dir(root)
	dependency := filepath.Join(base, "dependency")
	write(t, dependency, "go.mod", "module example.com/dependency\n\ngo 1.26\n")
	write(t, dependency, "value.go", "package dependency\nconst Value = 7\n")
	write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../missing-ignored-replacement\n")
	write(t, root, "app.go", "package app\nimport \"example.com/dependency\"\nvar Value = dependency.Value\n")
	write(t, base, "go.work", "go 1.26\nuse ./app\nreplace example.com/dependency => ./dependency\n")
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=" + filepath.Join(base, "go.work"))})
	if err != nil {
		t.Fatalf("workspace override was not preserved: %v", err)
	}
	defer stage.Close()
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "-mod=readonly", "./..."); err != nil {
		var selected string
		for _, entry := range stage.Environment() {
			if strings.HasPrefix(entry, "GOWORK=") {
				selected = strings.TrimPrefix(entry, "GOWORK=")
			}
		}
		data, _ := os.ReadFile(selected)
		t.Fatalf("%v\nroot: %s\nworkspace:\n%s", err, stage.Root(), data)
	}
	write(t, base, "go.work.sum", "new workspace checksum state\n")
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("workspace checksum creation was accepted: %v", err)
	}
}

func TestCreateRejectsSymbolicInputsAndPreservesExternalFiles(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprint(directory), func(t *testing.T) {
			root, parent := fixture(t)
			external := t.TempDir()
			write(t, external, "value", "EXTERNAL_PRIVATE_VALUE")
			target := filepath.Join(external, "value")
			if directory {
				target = external
			}
			if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
				t.Fatalf("create symbolic input: %v", err)
			}
			if stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")}); err == nil {
				stage.Close()
				t.Fatal("symbolic input was accepted")
			}
			if data, err := os.ReadFile(filepath.Join(external, "value")); err != nil || string(data) != "EXTERNAL_PRIVATE_VALUE" {
				t.Fatal("external file was modified")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("rejected copy leaked temporary output: %v, %v", entries, err)
			}
		})
	}
}

func TestClosePreservesReplacementDirectory(t *testing.T) {
	root, parent := fixture(t)
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")})
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Dir(stage.Root())
	saved := owned + "-saved"
	if err := os.Rename(owned, saved); err != nil {
		t.Fatal(err)
	}
	write(t, owned, "user-file", "USER_OWNED")
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatal("replaced staging pathname was accepted")
	}
	if err := stage.Close(); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("cleanup accepted another directory: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(owned, "user-file")); err != nil || string(data) != "USER_OWNED" {
		t.Fatal("cleanup removed replacement directory")
	}
	if err := os.Rename(owned, owned+"-user"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, owned); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatalf("cleanup could not retry its owned directory: %v", err)
	}
}

func TestCreatePreservesWorkspaceMemberReplacementsAndLaxDependencies(t *testing.T) {
	for _, mode := range []string{"member", "workspace-vendor", "lax-dependency"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			base := filepath.Dir(root)
			dependency := filepath.Join(base, "dependency")
			dependencyModule := "module example.com/dependency\n\ngo 1.26\n"
			if mode == "lax-dependency" {
				dependencyModule += "replace ignored invalid contents here\n"
			}
			write(t, dependency, "go.mod", dependencyModule)
			write(t, dependency, "value.go", "package dependency\nconst Value=1\n")
			replacement := "../dependency"
			if mode == "member" {
				replacement = "../missing-ignored"
			}
			write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v1.0.0\nreplace example.com/dependency => "+replacement+"\n")
			write(t, root, "app.go", "package app\nimport \"example.com/dependency\"\nvar Value=dependency.Value\n")
			env := environment("GOWORK=off")
			if mode != "lax-dependency" {
				uses := "use ./app\n"
				if mode == "member" {
					uses = "use (\n./app\n./dependency\n)\n"
				}
				write(t, base, "go.work", "go 1.26\n"+uses)
				env = environment("GOWORK=" + filepath.Join(base, "go.work"))
			}
			if mode == "workspace-vendor" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "work", "vendor"); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(base, "vendor", "modules.txt")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				write(t, base, "vendor/modules.txt", strings.ReplaceAll(string(data), "\n", "\r\n"))
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if err := gocommand.Run(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "./..."); err != nil {
				t.Fatal(err)
			}
			if err := stage.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecheckRejectsSymbolicStagedRoot(t *testing.T) {
	root, parent := fixture(t)
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")})
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	staged := stage.Root()
	if err := os.Rename(staged, staged+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, staged); err != nil {
		t.Fatal(err)
	}
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatal("symbolic staged Project root was accepted")
	}
}

func TestCreatePreservesVersionOverridesAndVendoring(t *testing.T) {
	for _, mode := range []string{"module", "workspace", "module-vendor", "workspace-vendor"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			base := filepath.Dir(root)
			for name, value := range map[string]int{"default": 1, "exact": 9} {
				dependency := filepath.Join(base, name)
				write(t, dependency, "go.mod", "module example.com/dependency\n\ngo 1.26\n")
				write(t, dependency, "value.go", fmt.Sprintf("package dependency\nconst Value = %d\n", value))
			}
			declarations := "replace (\nexample.com/dependency => ../default\nexample.com/dependency v1.0.0 => ../exact\n)\n"
			env := environment("GOWORK=off")
			if strings.HasPrefix(mode, "workspace") {
				declarations = ""
				write(t, base, "go.work", "go 1.26\nuse ./app\nreplace (\nexample.com/dependency => ./default\nexample.com/dependency v1.0.0 => ./exact\n)\n")
				env = environment("GOWORK=" + filepath.Join(base, "go.work"))
			}
			write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v1.0.0\n"+declarations)
			write(t, root, "app.go", "package app\nimport \"example.com/dependency\"\nvar _ [9]struct{} = [dependency.Value]struct{}{}\n")
			if strings.HasSuffix(mode, "vendor") {
				verb := "mod"
				if strings.HasPrefix(mode, "workspace") {
					verb = "work"
				}
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, verb, "vendor"); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, base)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			if err := gocommand.Run(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "./..."); err != nil {
				stage.Close()
				t.Fatal(err)
			}
			if err := stage.Recheck(t.Context()); err != nil {
				stage.Close()
				t.Fatal(err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("vendored staging changed original files")
			}
		})
	}
}

func TestCreatePreservesAliasedWorkspaceMemberReplacementBase(t *testing.T) {
	for _, mode := range []string{"missing-physical", "different-physical", "workspace-vendor", "aliased-workspace"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			base := filepath.Dir(root)
			member := filepath.Join(base, "physical", "member")
			dependency := filepath.Join(base, "dependency")
			write(t, member, "go.mod", "module example.com/member\n\ngo 1.26\nrequire example.com/dependency v1.0.0\nreplace example.com/dependency => ../dependency\n")
			write(t, member, "value.go", "package member\nimport \"example.com/dependency\"\nconst Value = dependency.Value\n")
			write(t, dependency, "go.mod", "module example.com/dependency\n\ngo 1.26\n")
			write(t, dependency, "value.go", "package dependency\nconst Value = 9\n")
			if mode != "missing-physical" {
				wrong := filepath.Join(base, "physical", "dependency")
				write(t, wrong, "go.mod", "module example.com/dependency\n\ngo 1.26\n")
				write(t, wrong, "value.go", "package dependency\nconst Value = 1\n")
			}
			if err := os.Symlink(member, filepath.Join(base, "alias")); err != nil {
				t.Fatal(err)
			}
			write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v1.0.0\n")
			write(t, root, "app.go", "package app\nimport \"example.com/member\"\nvar _ [9]struct{} = [member.Value]struct{}{}\n")
			work := filepath.Join(base, "go.work")
			if mode == "aliased-workspace" {
				physical := filepath.Join(base, "physical", "work")
				write(t, physical, "go.work", "go 1.26\nuse (\n ../app\n ../alias\n)\n")
				alias := filepath.Join(base, "workspace")
				if err := os.Symlink(physical, alias); err != nil {
					t.Fatal(err)
				}
				work = filepath.Join(alias, "go.work")
			} else {
				write(t, base, "go.work", "go 1.26\nuse (\n ./app\n ./alias\n)\n")
			}
			env := environment("GOWORK=" + work)
			if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "build", "./..."); err != nil {
				t.Fatalf("original Go selection: %v", err)
			}
			if mode == "workspace-vendor" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "work", "vendor"); err != nil {
					t.Fatal(err)
				}
			}
			beforeRoot, beforeMember, beforeDependency := tree(t, root), tree(t, member), tree(t, dependency)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if err := gocommand.Run(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "./..."); err != nil {
				t.Fatalf("staged Go selection: %v", err)
			}
			if err := stage.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeRoot, tree(t, root)) || !reflect.DeepEqual(beforeMember, tree(t, member)) || !reflect.DeepEqual(beforeDependency, tree(t, dependency)) {
				t.Fatal("aliased workspace staging changed source inputs")
			}
		})
	}
}

func TestRecheckDetectsRetargetedLocalReplacement(t *testing.T) {
	root, parent := fixture(t)
	base := filepath.Dir(root)
	for _, name := range []string{"first", "second"} {
		write(t, filepath.Join(base, name), "go.mod", "module example.com/dependency\n\ngo 1.26\n")
		write(t, filepath.Join(base, name), "value.go", "package dependency\n")
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "first"), alias); err != nil {
		t.Fatal(err)
	}
	write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v1.0.0\nreplace example.com/dependency => ../alias\n")
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off")})
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "second"), alias); err != nil {
		t.Fatal(err)
	}
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("retargeted replacement was accepted: %v", err)
	}
}

func TestCreateRejectsTemporaryParentInsideWorkspaceVendor(t *testing.T) {
	root, _ := fixture(t)
	base := filepath.Dir(root)
	write(t, base, "go.work", "go 1.26\nuse ./app\n")
	write(t, base, "vendor/modules.txt", "## workspace\n")
	vendor := filepath.Join(base, "vendor")
	before, err := os.Stat(vendor)
	if err != nil {
		t.Fatal(err)
	}
	if stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: vendor, Environment: environment("GOWORK=" + filepath.Join(base, "go.work"))}); err == nil {
		stage.Close()
		t.Fatal("temporary allocation inside a source vendor tree was accepted")
	}
	after, err := os.Stat(vendor)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("rejected staging wrote inside workspace vendor input")
	}
}

func fixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root, parent := filepath.Join(base, "app"), filepath.Join(base, "staging")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	write(t, root, "plystra.yaml", "{}\n")
	write(t, root, "app.go", "package app\n")
	return root, parent
}

func write(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		result[filepath.ToSlash(relative)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func environment(settings ...string) []string {
	var result []string
	for _, setting := range os.Environ() {
		key, _, _ := strings.Cut(setting, "=")
		if !strings.EqualFold(key, "GOWORK") && !strings.EqualFold(key, "GOFLAGS") {
			result = append(result, setting)
		}
	}
	return append(result, settings...)
}
