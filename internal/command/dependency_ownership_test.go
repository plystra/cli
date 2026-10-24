package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/dependencyadd"
	"github.com/plystra/cli/internal/dependencyremove"
	"github.com/plystra/cli/internal/dependencyupdate"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulemutation"
)

func TestDependencyPrimitiveUsesCapturedOwnership(t *testing.T) {
	for _, action := range []string{"update", "add"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			for _, change := range []string{"automatic constructor", "Resource parameter", "unknown parameter"} {
				t.Run(action+"/"+mode+"/"+change, func(t *testing.T) {
					t.Parallel()
					root, selected, environment := writeDependencyOwnershipProject(t, mode, change)
					before := commandTree(t, root)
					validated := false
					err := mutateDependencyOwnership(t, root, action, environment, func(ctx context.Context, directory string) error {
						validated = true
						return gocommand.Run(ctx, gocommand.Options{Directory: directory, Environment: environment}, "build", "-mod=readonly", "./...")
					})
					if change == "unknown parameter" {
						if err == nil || validated || !reflect.DeepEqual(before, commandTree(t, root)) {
							t.Fatalf("unproven old parameter was removed or inputs changed: err=%v validated=%t", err, validated)
						}
						return
					}
					if err != nil || !validated {
						t.Fatalf("dependency ownership cleanup failed: %v (validated=%t)", err, validated)
					}
					inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
					if err != nil {
						t.Fatal(err)
					}
					final, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
					if err != nil {
						t.Fatal(err)
					}
					if err := inputs.ValidateCandidate(final); err != nil {
						t.Fatalf("final application does not validate: %v", err)
					}
					if change == "automatic constructor" {
						owner, _ := constructorsymbol.Parse(previewDependencyModule + "/remote.New")
						if _, exists := final.Manifest().Configuration(owner); exists {
							t.Fatal("lost automatic owner's configuration survived")
						}
					} else if len(final.Manifest().ResourceBindings()) != 0 {
						t.Fatal("provably obsolete parameter binding survived")
					}
					after := commandTree(t, root)
					for _, path := range []string{"plystra.yaml", "plystra.production.yaml", "deploy/selected.yaml"} {
						if path != selected && !bytes.Equal(before[path], after[path]) {
							t.Fatalf("cleanup changed unselected document %s", path)
						}
					}
					code, _, stderr := runCommand(t, []string{"generate", "--check"}, root, environment)
					if code != 0 || stderr != "" {
						t.Fatalf("public generated consistency check failed: %d %s", code, stderr)
					}
					if mode == "environment" && change == "automatic constructor" {
						code, output, stderr := runCommand(t, []string{"inspect", "configuration", "--format", "json"}, root, environment)
						if code != 0 || stderr != inspectProgress || !json.Valid([]byte(output)) || strings.Contains(output+stderr, "PRIVATE_ENDPOINT") {
							t.Fatalf("suppressed old configuration inspection failed or disclosed a private value: %d %s", code, stderr)
						}
					}
					assertNoCommandTransactions(t, root)
				})
			}
		}
	}
}

func TestDependencyPrimitiveOwnershipRollback(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, _, environment := writeDependencyOwnershipProject(t, mode, "automatic constructor")
			before := commandTree(t, root)
			failure := errors.New("injected dependency verification failure")
			validated := false
			err := mutateDependencyOwnership(t, root, "update", environment, func(context.Context, string) error {
				validated = true
				return failure
			})
			if !errors.Is(err, failure) || !validated || !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatalf("dependency verification did not roll back the complete transaction: err=%v validated=%t", err, validated)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestDependencyPrimitiveRemovalRetainsRequiredOwner(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, _, environment := writeDependencyOwnershipProject(t, mode, "automatic constructor")
			before := commandTree(t, root)
			validated := false
			err := mutateDependencyOwnership(t, root, "remove", environment, func(context.Context, string) error {
				validated = true
				return nil
			})
			if err == nil || validated || !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatalf("removal discarded a required owner or changed inputs: err=%v validated=%t", err, validated)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestDependencyPrimitiveOwnershipPreservesConcurrentConfigurationEdit(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, selected, environment := writeDependencyOwnershipProject(t, mode, "automatic constructor")
			before := commandTree(t, root)
			var userEdit []byte
			err := mutateDependencyOwnership(t, root, "update", environment, func(context.Context, string) error {
				userEdit = append(readCommandFile(t, root, selected), []byte("\n# KEEP_CONCURRENT_USER_EDIT\n")...)
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(selected)), string(userEdit))
				return nil
			})
			if !errors.Is(err, atomicfs.ErrConcurrentChange) || len(userEdit) == 0 || !bytes.Equal(readCommandFile(t, root, selected), userEdit) {
				t.Fatalf("concurrent selected-document edit was accepted or overwritten: %v", err)
			}
			if paths := atomicfs.ConcurrentChangePaths(err); !reflect.DeepEqual(paths, []string{selected}) {
				t.Fatalf("concurrent configuration paths = %v, want %s", paths, selected)
			}
			backups, backupErr := filepath.Glob(filepath.Join(root, ".plystra-files-*", "backup", "*"))
			if backupErr != nil || len(backups) != 1 {
				t.Fatalf("original selected-document recovery backup was not retained: %v %v", backups, backupErr)
			}
			backupPath, err := filepath.Rel(root, backups[0])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(readCommandFile(t, root, filepath.ToSlash(backupPath)), before[selected]) {
				t.Fatal("selected-document recovery backup lost its original bytes")
			}
			after := commandTree(t, root)
			delete(after, filepath.ToSlash(backupPath))
			delete(before, selected)
			delete(after, selected)
			if !reflect.DeepEqual(before, after) {
				for path, data := range before {
					if !bytes.Equal(data, after[path]) {
						t.Logf("transaction path not restored: %s", path)
					}
				}
				for path := range after {
					if _, existed := before[path]; !existed {
						t.Logf("unexpected transaction path: %s", path)
					}
				}
				t.Fatal("unmodified transaction paths were not restored after concurrent edit")
			}
		})
	}
}

func TestDependencyPrimitiveOwnershipIgnoresUnselectedInputs(t *testing.T) {
	for _, name := range []string{"plystra.unselected.yaml", "sandbox/ignored.go"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, _, environment := writeDependencyOwnershipProject(t, "default", "automatic constructor")
			if name == "sandbox/ignored.go" {
				writeCommandFile(t, filepath.Join(root, "sandbox/go.mod"), "module example.com/unrelated\n\ngo 1.26\n")
			}
			writeCommandFile(t, filepath.Join(root, filepath.FromSlash(name)), "ignored input\n")
			userEdit := "unselected concurrent user edit\n"
			err := mutateDependencyOwnership(t, root, "update", environment, func(ctx context.Context, directory string) error {
				if err := gocommand.Run(ctx, gocommand.Options{Directory: directory, Environment: environment}, "build", "-mod=readonly", "./..."); err != nil {
					return err
				}
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(name)), userEdit)
				return nil
			})
			if err != nil || string(readCommandFile(t, root, name)) != userEdit {
				t.Fatalf("unselected input affected dependency ownership: %v", err)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeDependencyOwnershipProject(t *testing.T, mode, change string) (string, string, []string) {
	t.Helper()
	old, next := previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", false)
	owner := previewDependencyModule + "/remote.New"
	document := "interfaces:\n  require: [reports.read/v1, remote.echo/v1]\nconfig:\n  " + owner + ": {endpoint: PRIVATE_ENDPOINT}\n"
	if change == "automatic constructor" {
		next.sources["remote/implementation.go"] = strings.Replace(next.sources["remote/implementation.go"], "func New(Config)", "func Replacement()", 1)
	} else {
		for _, candidate := range []*commandProxyModule{&old, &next} {
			candidate.sources["database/resource.go"] = "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Value() string }\n"
			candidate.sources["databaseprovider/implementation.go"] = "package databaseprovider\ntype Pool struct{}\n//plystra:implements-resource storage.database/v1\nfunc New() (*Pool,error) { panic(\"MUST_NOT_CONSTRUCT\") }\nfunc (*Pool) Value() string { return \"\" }\n"
		}
		old.sources["remote/implementation.go"] = strings.Replace(old.sources["remote/implementation.go"], `"context"`, `"context"; db "example.com/preview/remote/database"`, 1)
		old.sources["remote/implementation.go"] = strings.Replace(old.sources["remote/implementation.go"], "func New(Config)", "func New(config Config, database db.Resource)", 1)
		document += "resources:\n  instances:\n    primary:\n      use: " + previewDependencyModule + "/databaseprovider.New\n  bind:\n    implementations:\n      " + owner + ": {database: primary}\n"
	}
	root := writeImplementationSelectionCommandProject(t)
	environment := previewDependencyEnvironment(t, writeCommandDependencyProxy(t, []commandProxyModule{old, next}))
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PLYSTRA_ENV") && !strings.EqualFold(name, "PLYSTRA_CONFIG") {
			filtered = append(filtered, entry)
		}
	}
	environment = filtered
	selected := "plystra.yaml"
	writeCommandFile(t, filepath.Join(root, selected), document)
	if mode == "environment" {
		selected = "plystra.production.yaml"
		writeCommandFile(t, filepath.Join(root, selected), "# KEEP_SELECTED_OVERLAY\n{}\n")
		environment = append(environment, "PLYSTRA_ENV=production")
	} else if mode == "replacement" {
		selected = "deploy/selected.yaml"
		writeCommandFile(t, filepath.Join(root, selected), document)
		environment = append(environment, "PLYSTRA_CONFIG="+filepath.Join(root, filepath.FromSlash(selected)))
	}
	seedPreviewDependency(t, root, environment, "v1.0.0")
	if err := modulemutation.Tidy(t.Context(), root, "", environment, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, MutateModule: mutate,
			Validate: func(ctx context.Context, directory string) error {
				return gocommand.Run(ctx, gocommand.Options{Directory: directory, Environment: environment}, "build", "-mod=readonly", "./...")
			},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if change == "unknown parameter" {
		if mode == "environment" {
			writeCommandFile(t, filepath.Join(root, selected), "resources:\n  bind:\n    implementations:\n      "+owner+":\n        database: {$remove: true}\n        unowned: primary\n")
		} else {
			writeCommandFile(t, filepath.Join(root, selected), strings.Replace(document, "{database: primary}", "{unowned: primary}", 1))
		}
	}
	return root, selected, environment
}

func mutateDependencyOwnership(t *testing.T, root, action string, environment []string, validate applicationgenerate.Validator) error {
	t.Helper()
	start := filepath.Join(root, "reports")
	query := previewDependencyModule + "@v1.1.0"
	if action == "remove" {
		_, err := dependencyremove.Remove(t.Context(), dependencyremove.Options{Start: start, ModulePath: previewDependencyModule, Environment: environment, Validate: validate})
		return err
	}
	if action == "add" {
		_, err := dependencyadd.Add(t.Context(), dependencyadd.Options{Start: start, Query: query, Environment: environment, Validate: validate})
		return err
	}
	_, err := dependencyupdate.Update(t.Context(), dependencyupdate.Options{Start: start, Query: query, Environment: environment, Validate: validate})
	return err
}
