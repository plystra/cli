package implementationcreate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationcreate"
	"github.com/plystra/cli/internal/interfacecreate"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectstage"
)

func TestMaterializeBatchResolvesContractsBeforePendingImplementations(t *testing.T) {
	var forward map[string]fileState
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			root := newProject(t, "example.com/app")
			writeFile(t, filepath.Join(root, "database", "resource.go"), `package database
import "context"
type Result struct { Value string }
type Embedded interface { Name() string }
//plystra:resource storage.database/v1
type Resource interface {
 Embedded
 Health(context.Context) error
 Read(...string) (Result, error)
}
`)
			writeFile(t, filepath.Join(root, "authored", "service.go"), `package authored
import (
 "context"
 contract "example.com/app/interfaces/email/send/v1"
 pending "example.com/app/zsmtp"
 provider "example.com/app/adb"
)
var _ *pending.Service
var _ *provider.Provider
type Service struct{}
//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
			writeFile(t, filepath.Join(root, "authoredprovider", "provider.go"), `package authoredprovider
import (
 "context"
 contract "example.com/app/database"
 pending "example.com/app/zsmtp"
)
var _ *pending.Service
type Provider struct{}
//plystra:implements-resource storage.database/v1
func New() (*Provider, error) { return &Provider{}, nil }
func (*Provider) Name() string { return "authored" }
func (*Provider) Health(context.Context) error { return nil }
func (*Provider) Read(...string) (contract.Result, error) { return contract.Result{}, nil }
`)
			before := snapshotTree(t, root)
			stage := newMaterializeStage(t, root)
			if _, err := interfacecreate.Materialize(t.Context(), stage, "email.send/v1", interfacemeta.OperationKindCommand); err != nil {
				t.Fatal(err)
			}
			parentInfo, err := os.Stat(stage.Root())
			if err != nil {
				t.Fatal(err)
			}
			requests := []implementationcreate.StageRequest{{ContractID: "email.send/v1", Package: "./zsmtp"}, {ContractID: "storage.database/v1", Package: "./adb"}}
			if reverse {
				requests[0], requests[1] = requests[1], requests[0]
			}
			requestCopy := append([]implementationcreate.StageRequest(nil), requests...)
			results, err := implementationcreate.Materialize(t.Context(), stage, requests)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(requests, requestCopy) || len(results) != 2 || results[0].Kind() != "resource" || results[1].Kind() != "interface" {
				t.Fatalf("batch results or request ownership = %#v, %#v", results, requests)
			}
			for index, result := range results {
				name, id := "adb", "storage.database/v1"
				if index == 1 {
					name, id = "zsmtp", "email.send/v1"
				}
				if result.ContractID().String() != id || result.ModuleRoot() != stage.Root() || result.PackagePath() != filepath.Join(stage.Root(), name) || result.SourcePath() != name+"/implementation.go" || result.ImportPath() != "example.com/app/"+name || result.Constructor().String() != "example.com/app/"+name+".New" {
					t.Fatalf("staged identity = %#v", result)
				}
				file, err := os.Open(filepath.Join(stage.Root(), filepath.FromSlash(result.SourcePath())))
				if err != nil {
					t.Fatal(err)
				}
				checkErr, closeErr := privatefile.Check(file), file.Close()
				if checkErr != nil || closeErr != nil {
					t.Fatalf("scaffold is not private: %v, %v", checkErr, closeErr)
				}
			}
			afterInfo, err := os.Stat(stage.Root())
			if err != nil || !os.SameFile(parentInfo, afterInfo) || parentInfo.Mode() != afterInfo.Mode() {
				t.Fatalf("private stage root permissions changed: %v", err)
			}
			if got := snapshotTree(t, stage.Root()); !reverse {
				forward = got
			} else if !reflect.DeepEqual(forward, got) {
				t.Fatal("batch order changed scaffold bytes")
			}
			writeFile(t, filepath.Join(stage.Root(), "zsmtp", "implementation_test.go"), `package zsmtp
import (
 "context"
 "testing"
 contract "example.com/app/interfaces/email/send/v1"
)
func TestUnfinished(t *testing.T) {
 value, err := New()
 if err != nil || value == nil { t.Fatal("constructor failed") }
 _, err = value.Send(context.Background(), contract.Request{})
 if err == nil || err.Error() != "email.send/v1 implementation is not implemented" { t.Fatalf("operation error = %v", err) }
}
`)
			writeFile(t, filepath.Join(stage.Root(), "adb", "implementation_test.go"), `package adb
import "testing"
func TestUnfinished(t *testing.T) {
 value, err := New()
 if value != nil || err == nil || err.Error() != "storage.database/v1 Resource provider is not implemented" { t.Fatalf("constructor = %v, %v", value, err) }
 defer func() { if recover() != err { t.Fatal("method did not retain deterministic failure") } }()
 new(Provider).Name()
}
`)
			// This is the first complete-application type check, after every
			// requested package exists; it also executes both non-success bodies.
			if _, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "test", "-mod=readonly", "./..."); err != nil {
				t.Fatalf("complete candidate: %v", err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("materialization changed original authored source")
			}
		})
	}
}

func TestMaterializeBatchUsesStageEnvironment(t *testing.T) {
	root := newProject(t, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "selected.go"), "//go:build stage_contract\n\n"+interfaceSource("api", "email.send/v2", "Send"))
	writeFile(t, filepath.Join(root, "api", "other.go"), "//go:build !stage_contract\n\npackage api\n//plystra:interface email.send/v2\ntype Interface int\n")
	stage := newMaterializeStage(t, root, "GOFLAGS=-tags=stage_contract")
	t.Setenv("GOFLAGS", "-tags=other_contract")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "absent.work"))
	results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{{ContractID: "email.send/v2", Package: "./smtp"}})
	if err != nil || len(results) != 1 || results[0].ContractID().Major() != 2 {
		t.Fatalf("captured environment was not authoritative: %#v, %v", results, err)
	}
}

func TestMaterializeBatchUsesRelocatedDependencyContracts(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(fmt.Sprintf("workspace=%t", workspace), func(t *testing.T) {
			base := t.TempDir()
			root, dependency := filepath.Join(base, "app"), filepath.Join(base, "contracts")
			writeProject(t, root, "example.com/app")
			writeProject(t, dependency, "example.com/contracts")
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/contracts v1.0.0\nreplace example.com/contracts => ../contracts\n")
			writeFile(t, filepath.Join(dependency, "api", "interface.go"), interfaceSource("api", "email.send/v2", "Send"))
			writeFile(t, filepath.Join(dependency, "database", "resource.go"), "package database\n//plystra:resource storage.database/v3\ntype Resource interface { Read(...int) (<-chan string, error) }\n")
			settings := []string{}
			if workspace {
				work := filepath.Join(base, "go.work")
				writeFile(t, work, "go 1.26\nuse (\n ./app\n ./contracts\n)\n")
				settings = append(settings, "GOWORK="+work)
			}
			originalBefore, dependencyBefore := snapshotTree(t, root), snapshotTree(t, dependency)
			stage := newMaterializeStage(t, root, settings...)
			results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{{ContractID: "email.send/v2", Package: "./smtp"}, {ContractID: "storage.database/v3", Package: "./database"}})
			if err != nil || len(results) != 2 {
				t.Fatalf("relocated contracts: %#v, %v", results, err)
			}
			if _, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "-mod=readonly", "./..."); err != nil {
				t.Fatalf("relocated candidate: %v", err)
			}
			if !reflect.DeepEqual(originalBefore, snapshotTree(t, root)) || !reflect.DeepEqual(dependencyBefore, snapshotTree(t, dependency)) {
				t.Fatal("materialization changed original dependency or Project source")
			}
			stagedBefore := snapshotStageTree(t, stage.Root())
			writeFile(t, filepath.Join(dependency, "user.txt"), "DEPENDENCY_EDIT\n")
			if results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{{ContractID: "email.send/v2", Package: "./another"}}); !errors.Is(err, projectstage.ErrConcurrentChange) || results != nil {
				t.Fatalf("dependency drift accepted: %#v, %v", results, err)
			}
			if !reflect.DeepEqual(stagedBefore, snapshotStageTree(t, stage.Root())) {
				t.Fatal("dependency drift rejection changed stage")
			}
		})
	}
}

func TestMaterializeBatchOwnsRequestsDuringDiscovery(t *testing.T) {
	root := newProject(t, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
	stage := newMaterializeStage(t, root)
	requests := []implementationcreate.StageRequest{{ContractID: "email.send/v1", Package: "./smtp"}}
	calls := 0
	ctx := &batchCheckContext{Context: t.Context(), check: func() {
		calls++
		if calls == 2 {
			requests[0] = implementationcreate.StageRequest{ContractID: "missing.contract/v1", Package: "./caller_edit"}
		}
	}}
	results, err := implementationcreate.Materialize(ctx, stage, requests)
	if err != nil || len(results) != 1 || results[0].SourcePath() != "smtp/implementation.go" {
		t.Fatalf("caller-owned request edit changed staged batch: %#v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(stage.Root(), "caller_edit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized caller edit: %v", err)
	}
}

func TestMaterializeBatchRejectsInvalidInputsAndTargets(t *testing.T) {
	root := newProject(t, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
	stage := newMaterializeStage(t, root)
	before := snapshotStageTree(t, stage.Root())
	valid := implementationcreate.StageRequest{ContractID: "email.send/v1", Package: "./smtp"}
	for _, requests := range [][]implementationcreate.StageRequest{nil, {}, make([]implementationcreate.StageRequest, 1_025)} {
		if results, err := implementationcreate.Materialize(t.Context(), stage, requests); !errors.Is(err, implementationcreate.ErrCreate) || results != nil {
			t.Fatalf("invalid batch bounds: %#v, %v", results, err)
		}
	}
	for _, id := range []string{"", "email.send", "email.send/v0", "email.send/v01", "email.send/v1 ", strings.Repeat("a", 4_097) + ".send/v1"} {
		request := valid
		request.ContractID = id
		if results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{request}); !errors.Is(err, implementationcreate.ErrInvalidContract) || results != nil {
			t.Fatalf("invalid contract ID: %#v, %v", results, err)
		}
	}
	for _, target := range []string{"", ".", "smtp", "../smtp", "./../smtp", "./a/../smtp", "./smtp/", "./generated/smtp", "./testdata/smtp", "./smtp-name", `./a\smtp`, "./" + strings.Repeat("a/", 512) + "smtp"} {
		request := valid
		request.Package = target
		if results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{request}); !errors.Is(err, implementationcreate.ErrInvalidPackage) || results != nil {
			t.Fatalf("invalid package %q: %#v, %v", target, results, err)
		}
	}
	for _, targets := range [][]string{{"./smtp", "./smtp"}, {"./smtp", "./smtp/child"}, {"./smtp/child", "./smtp"}, {"./smtp", "./smtp-other", "./smtp/child"}} {
		requests := make([]implementationcreate.StageRequest, len(targets))
		for index, target := range targets {
			requests[index] = implementationcreate.StageRequest{ContractID: valid.ContractID, Package: target}
		}
		if results, err := implementationcreate.Materialize(t.Context(), stage, requests); !errors.Is(err, implementationcreate.ErrInvalidPackage) || results != nil {
			t.Fatalf("duplicate or overlapping batch: %#v, %v", results, err)
		}
	}
	if runtime.GOOS == "windows" {
		requests := []implementationcreate.StageRequest{valid, {ContractID: valid.ContractID, Package: "./SMTP"}}
		if results, err := implementationcreate.Materialize(t.Context(), stage, requests); !errors.Is(err, implementationcreate.ErrInvalidPackage) || results != nil {
			t.Fatalf("case-alias duplicate batch: %#v, %v", results, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if results, err := implementationcreate.Materialize(ctx, stage, []implementationcreate.StageRequest{valid}); !errors.Is(err, implementationcreate.ErrCreate) || results != nil || ctx != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("invalid context: %#v, %v", results, err)
		}
	}
	closed := newMaterializeStage(t, root)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*projectstage.Stage{nil, {}, closed} {
		if results, err := implementationcreate.Materialize(t.Context(), invalid, []implementationcreate.StageRequest{valid}); !errors.Is(err, projectstage.ErrConcurrentChange) || results != nil {
			t.Fatalf("invalid stage: %#v, %v", results, err)
		}
	}
	if !reflect.DeepEqual(before, snapshotStageTree(t, stage.Root())) {
		t.Fatal("invalid input changed staged source")
	}
}

func TestMaterializeBatchRejectsInvisibleAmbiguousAndInvalidContracts(t *testing.T) {
	for _, failure := range []string{"absent", "duplicate interface", "duplicate resource", "ambiguous kind", "invalid type", "sealed resource"} {
		t.Run(failure, func(t *testing.T) {
			root := newProject(t, "example.com/app")
			writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
			contractID, want := "email.send/v1", implementationcreate.ErrContractNotFound
			switch failure {
			case "absent":
				contractID = "missing.contract/v1"
			case "duplicate interface":
				writeFile(t, filepath.Join(root, "duplicate", "interface.go"), interfaceSource("duplicate", contractID, "Send"))
				want = interfaceinventory.ErrDuplicateID
			case "duplicate resource":
				for _, name := range []string{"first", "second"} {
					writeFile(t, filepath.Join(root, name, "resource.go"), "package "+name+"\n//plystra:resource storage.database/v1\ntype Resource interface { Health() error }\n")
				}
				contractID, want = "storage.database/v1", interfaceinventory.ErrDuplicateResourceID
			case "ambiguous kind":
				writeFile(t, filepath.Join(root, "resource", "resource.go"), "package resource\n//plystra:resource email.send/v1\ntype Resource interface { Health() error }\n")
				want = implementationcreate.ErrAmbiguousContract
			case "invalid type":
				writeFile(t, filepath.Join(root, "api", "extra.go"), "package api\nvar _ Undefined\n")
				want = interfaceinventory.ErrDiscover
			case "sealed resource":
				writeFile(t, filepath.Join(root, "resource", "resource.go"), "package resource\n//plystra:resource storage.database/v1\ntype Resource interface { private() }\n")
				contractID, want = "storage.database/v1", implementationcreate.ErrUnimplementableContract
			}
			stage := newMaterializeStage(t, root)
			before := snapshotStageTree(t, stage.Root())
			results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{{ContractID: "email.send/v1", Package: "./a_smtp"}, {ContractID: contractID, Package: "./z_other"}})
			if !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, want) || results != nil {
				t.Fatalf("invalid contract batch: %#v, %v; want %v", results, err, want)
			}
			if !reflect.DeepEqual(before, snapshotStageTree(t, stage.Root())) {
				t.Fatal("failed batch partially wrote scaffolds")
			}
		})
	}
}

func TestMaterializeBatchRejectsCollisionsUnsafePathsAndDrift(t *testing.T) {
	for _, failure := range []string{"empty target", "occupied target", "symbolic parent", "symbolic target", "drift"} {
		t.Run(failure, func(t *testing.T) {
			root := newProject(t, "example.com/app")
			writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
			stage := newMaterializeStage(t, root)
			external := t.TempDir()
			writeFile(t, filepath.Join(external, "keep.txt"), "EXTERNAL\n")
			externalBefore := snapshotTree(t, external)
			target, want := "./z_smtp", implementationcreate.ErrTargetExists
			switch failure {
			case "empty target":
				if err := os.Mkdir(filepath.Join(stage.Root(), "z_smtp"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "occupied target":
				writeFile(t, filepath.Join(stage.Root(), "z_smtp", "keep.txt"), "AUTHORED\n")
			case "symbolic parent":
				if err := os.Symlink(external, filepath.Join(stage.Root(), "link")); err != nil {
					t.Fatal(err)
				}
				target, want = "./link/smtp", nil
			case "symbolic target":
				if err := os.Symlink(external, filepath.Join(stage.Root(), "z_smtp")); err != nil {
					t.Fatal(err)
				}
			case "drift":
				writeFile(t, filepath.Join(root, "user.txt"), "USER_EDIT\n")
				want = projectstage.ErrConcurrentChange
			}
			before, originalBefore := snapshotStageTree(t, stage.Root()), snapshotTree(t, root)
			results, err := implementationcreate.Materialize(t.Context(), stage, []implementationcreate.StageRequest{{ContractID: "email.send/v1", Package: "./a_smtp"}, {ContractID: "email.send/v1", Package: target}})
			if !errors.Is(err, implementationcreate.ErrCreate) || results != nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("unsafe batch: %#v, %v", results, err)
			}
			if !reflect.DeepEqual(before, snapshotStageTree(t, stage.Root())) || !reflect.DeepEqual(originalBefore, snapshotTree(t, root)) || !reflect.DeepEqual(externalBefore, snapshotTree(t, external)) {
				t.Fatal("failed batch changed staged, original, or external source")
			}
		})
	}
}

func TestMaterializeBatchRollsBackAllWritesAfterLateFailure(t *testing.T) {
	for _, failure := range []string{"cancellation", "drift"} {
		t.Run(failure, func(t *testing.T) {
			root := newProject(t, "example.com/app")
			writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
			stage := newMaterializeStage(t, root)
			before := snapshotStageTree(t, stage.Root())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observed := false
			checked := &batchCheckContext{Context: ctx, check: func() {
				if observed {
					return
				}
				for _, name := range []string{"a", "b"} {
					if _, err := os.Stat(filepath.Join(stage.Root(), name, "implementation.go")); err != nil {
						return
					}
				}
				observed = true
				if failure == "cancellation" {
					cancel()
				} else {
					writeFile(t, filepath.Join(root, "user.txt"), "USER_EDIT\n")
				}
			}}
			results, err := implementationcreate.Materialize(checked, stage, []implementationcreate.StageRequest{{ContractID: "email.send/v1", Package: "./a"}, {ContractID: "email.send/v1", Package: "./b"}})
			want := error(context.Canceled)
			if failure == "drift" {
				want = projectstage.ErrConcurrentChange
			}
			if !observed || !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, want) || results != nil {
				t.Fatalf("late failure: observed=%t, results=%#v, err=%v", observed, results, err)
			}
			if !reflect.DeepEqual(before, snapshotStageTree(t, stage.Root())) {
				t.Fatal("failed atomic batch left files, directories, or transaction residue")
			}
			if failure == "drift" && readFile(t, filepath.Join(root, "user.txt")) != "USER_EDIT\n" {
				t.Fatal("rollback discarded original source edits")
			}
		})
	}
}

func TestMaterializeBatchAcceptsMaximumRequests(t *testing.T) {
	root := newProject(t, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
	stage := newMaterializeStage(t, root)
	requests := make([]implementationcreate.StageRequest, implementationcreate.MaximumStageRequests)
	for index := range requests {
		requests[index] = implementationcreate.StageRequest{ContractID: "email.send/v1", Package: fmt.Sprintf("./implementations/p%04d", len(requests)-index-1)}
	}
	results, err := implementationcreate.Materialize(t.Context(), stage, requests)
	if err != nil || len(results) != len(requests) {
		t.Fatalf("maximum batch: count=%d, err=%v", len(results), err)
	}
	for _, result := range results {
		if !strings.Contains(readFile(t, filepath.Join(stage.Root(), filepath.FromSlash(result.SourcePath()))), "return contract.Response{}, errNotImplemented") {
			t.Fatal("maximum batch omitted an unfinished scaffold")
		}
	}
}

type batchCheckContext struct {
	context.Context
	mu    sync.Mutex
	check func()
}

func (c *batchCheckContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.check()
	return c.Context.Err()
}

func newMaterializeStage(t *testing.T, root string, settings ...string) *projectstage.Stage {
	t.Helper()
	environment := append(goEnvironment(), "GOFLAGS=")
	environment = append(environment, settings...)
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: t.TempDir(), Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close stage: %v", err)
		}
	})
	return stage
}

func snapshotStageTree(t testing.TB, root string) map[string]fileState {
	t.Helper()
	result := make(map[string]fileState)
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var data string
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			data, err = os.Readlink(name)
		case !info.IsDir():
			var contents []byte
			contents, err = os.ReadFile(name)
			data = string(contents)
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = fileState{data: data, mode: info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
