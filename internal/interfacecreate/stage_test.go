package interfacecreate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectstage"
)

func TestMaterializeStagesCompleteCandidateInEitherOrder(t *testing.T) {
	var forward []string
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			original := materializeProject(t)
			writeRollbackFile(t, filepath.Join(original, "authored.go"), `package app
import (
    create "example.com/app/interfaces/order/create/v1"
    reset "example.com/app/interfaces/account/reset-password/v1"
)
var _ create.Request
var _ reset.Response
`)
			writeRollbackFile(t, filepath.Join(original, "interfaces", "order", "keep.txt"), "AUTHORED_ASSET\n")
			before := snapshotTree(t, original)
			stage := materializeStage(t, original)
			parents := []string{stage.Root(), filepath.Dir(stage.Root()), filepath.Join(stage.Root(), "interfaces"), filepath.Join(stage.Root(), "interfaces", "order")}
			infos := make([]os.FileInfo, len(parents))
			for index, parent := range parents {
				info, err := os.Stat(parent)
				if err != nil {
					t.Fatal(err)
				}
				infos[index] = info
			}
			operations := []struct {
				name string
				kind interfacemeta.OperationKind
			}{
				{"order.create/v1", interfacemeta.OperationKindCommand},
				{"account.reset-password/v1", interfacemeta.OperationKindQuery},
			}
			if reverse {
				operations[0], operations[1] = operations[1], operations[0]
			}
			for _, operation := range operations {
				result, err := Materialize(t.Context(), stage, operation.name, operation.kind)
				if err != nil {
					t.Fatal(err)
				}
				parts := strings.Split(strings.TrimSuffix(operation.name, "/v1"), ".")
				directory := path.Join(append(append([]string{"interfaces"}, parts...), "v1")...)
				if result.ID().String() != operation.name || result.ModuleRoot() != stage.Root() || result.PackagePath() != filepath.Join(stage.Root(), filepath.FromSlash(directory)) || result.ImportPath() != "example.com/app/"+directory || result.SourcePath() != directory+"/interface.go" {
					t.Fatalf("staged identity = %#v", result)
				}
				metadataPath := directory + "/interface.yaml"
				data, err := os.ReadFile(filepath.Join(stage.Root(), filepath.FromSlash(metadataPath)))
				if err != nil || string(data) != "semantics:\n  kind: "+string(operation.kind)+"\n" {
					t.Fatalf("metadata = %q, %v", data, err)
				}
				document, err := interfacemeta.ParseFile(metadataPath, data)
				if err != nil {
					t.Fatal(err)
				}
				semantics, ok := document.Semantics()
				if !ok || semantics.Kind() != operation.kind || document.Path() != metadataPath {
					t.Fatalf("metadata semantics = %v, %t", semantics.Kind(), ok)
				}
				for _, name := range []string{"interface.go", "interface.yaml"} {
					file, err := os.Open(filepath.Join(result.PackagePath(), name))
					if err != nil {
						t.Fatal(err)
					}
					checkErr := privatefile.Check(file)
					closeErr := file.Close()
					if checkErr != nil || closeErr != nil {
						t.Fatalf("staged %s is not owner-private: %v, %v", name, checkErr, closeErr)
					}
				}
			}
			wantSource := `package createv1

import "context"

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

// Request contains the order.create/v1 request fields.
type Request struct{}

// Response contains the order.create/v1 response fields.
type Response struct{}
`
			data, err := os.ReadFile(filepath.Join(stage.Root(), "interfaces", "order", "create", "v1", "interface.go"))
			if err != nil || string(data) != wantSource {
				t.Fatalf("source = %q, %v", data, err)
			}
			for index, parent := range parents {
				after, err := os.Stat(parent)
				if err != nil || !os.SameFile(infos[index], after) || infos[index].Mode() != after.Mode() {
					t.Fatalf("existing private parent changed: %s: %v", parent, err)
				}
			}
			if err := stage.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Only the complete candidate is built; both imports were unresolved
			// when the first Interface was materialized.
			if _, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "build", "-mod=readonly", "./..."); err != nil {
				t.Fatalf("complete candidate build: %v", err)
			}
			if got := snapshotTree(t, stage.Root()); !reverse {
				forward = got
			} else if !reflect.DeepEqual(got, forward) {
				t.Fatalf("request order changed staged bytes:\nforward: %v\nreverse: %v", forward, got)
			}
			if !reflect.DeepEqual(snapshotTree(t, original), before) {
				t.Fatal("materialization changed original source")
			}
		})
	}
}

func TestMaterializeRejectsInvalidInputs(t *testing.T) {
	original := materializeProject(t)
	stage := materializeStage(t, original)
	before := snapshotTree(t, stage.Root())
	for _, name := range []string{"", "order", "order.create", "Order.create/v1", "order.create/v0", "order.create/v2", "order.create/v01", "order.create/v+1", "order.create/v1/extra", "order..create/v1", "../order.create/v1", `order\create/v1`, "order.create/v1 ", strings.Repeat("a", 4_097) + ".create/v1"} {
		if result, err := Materialize(t.Context(), stage, name, interfacemeta.OperationKindQuery); !errors.Is(err, ErrCreate) || !errors.Is(err, ErrInvalidName) || result != (Result{}) {
			t.Fatalf("invalid name accepted: result=%#v, err=%v", result, err)
		}
	}
	for _, kind := range []interfacemeta.OperationKind{"", "Query", "event", "stream", "command ", "query\nerrors: []"} {
		if result, err := Materialize(t.Context(), stage, "order.create/v1", kind); !errors.Is(err, ErrCreate) || !errors.Is(err, interfacemeta.ErrInvalidSemantics) || result != (Result{}) {
			t.Fatalf("invalid semantics %q: result=%#v, err=%v", kind, result, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, expire := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer expire()
	for _, ctx := range []context.Context{nil, canceled, expired} {
		result, err := Materialize(ctx, stage, "order.create/v1", interfacemeta.OperationKindQuery)
		if !errors.Is(err, ErrCreate) || result != (Result{}) || ctx != nil && !errors.Is(err, ctx.Err()) {
			t.Fatalf("invalid context: result=%#v, err=%v", result, err)
		}
	}
	closed := materializeStage(t, original)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*projectstage.Stage{nil, {}, closed} {
		if result, err := Materialize(t.Context(), invalid, "order.create/v1", interfacemeta.OperationKindQuery); !errors.Is(err, ErrCreate) || !errors.Is(err, projectstage.ErrConcurrentChange) || result != (Result{}) {
			t.Fatalf("invalid stage: result=%#v, err=%v", result, err)
		}
	}
	if !reflect.DeepEqual(snapshotTree(t, stage.Root()), before) {
		t.Fatal("invalid input mutated stage")
	}
}

func TestMaterializeRejectsOccupiedTargets(t *testing.T) {
	for _, kind := range []string{"empty", "authored", "materialized", "file"} {
		t.Run(kind, func(t *testing.T) {
			original := materializeProject(t)
			stage := materializeStage(t, original)
			target := filepath.Join(stage.Root(), "interfaces", "order", "create", "v1")
			switch kind {
			case "materialized":
				if _, err := Materialize(t.Context(), stage, "order.create/v1", interfacemeta.OperationKindQuery); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeRollbackFile(t, target, "AUTHORED_FILE\n")
			default:
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "authored" {
					writeRollbackFile(t, filepath.Join(target, "interface.yaml"), "AUTHORED_METADATA\n")
				}
			}
			before := snapshotTree(t, stage.Root())
			result, err := Materialize(t.Context(), stage, "order.create/v1", interfacemeta.OperationKindCommand)
			var conflict *TargetExistsError
			if !errors.Is(err, ErrCreate) || !errors.Is(err, ErrTargetExists) || !errors.As(err, &conflict) || result != (Result{}) {
				t.Fatalf("occupied target: result=%#v, err=%v", result, err)
			}
			if conflict.ModulePath() != "example.com/app" || conflict.SourcePath() != "interfaces/order/create/v1" || conflict.SourceKind() != "authored-package" || conflict.Line() != 0 {
				t.Fatalf("target provenance = %#v", conflict)
			}
			if !reflect.DeepEqual(snapshotTree(t, stage.Root()), before) {
				t.Fatal("collision changed staged source")
			}
		})
	}
}

func TestMaterializeRejectsOriginalInputDrift(t *testing.T) {
	for _, change := range []string{"bytes", "membership", "module"} {
		t.Run(change, func(t *testing.T) {
			original := materializeProject(t)
			writeRollbackFile(t, filepath.Join(original, "asset.txt"), "ORIGINAL\n")
			stage := materializeStage(t, original)
			before := snapshotTree(t, stage.Root())
			switch change {
			case "bytes":
				writeRollbackFile(t, filepath.Join(original, "asset.txt"), "USER_EDIT\n")
			case "membership":
				writeRollbackFile(t, filepath.Join(original, "new.txt"), "USER_ADDITION\n")
			case "module":
				writeRollbackFile(t, filepath.Join(original, "go.mod"), "module example.com/changed\n\ngo 1.26\n")
			}
			changed := snapshotTree(t, original)
			result, err := Materialize(t.Context(), stage, "order.create/v1", interfacemeta.OperationKindCommand)
			if !errors.Is(err, ErrCreate) || !errors.Is(err, projectstage.ErrConcurrentChange) || result != (Result{}) {
				t.Fatalf("original drift: result=%#v, err=%v", result, err)
			}
			if !reflect.DeepEqual(snapshotTree(t, stage.Root()), before) || !reflect.DeepEqual(snapshotTree(t, original), changed) {
				t.Fatal("drift rejection changed source or stage")
			}
		})
	}
}

func TestMaterializeRejectsStageRootRedirectedToOriginal(t *testing.T) {
	original := materializeProject(t)
	stage := materializeStage(t, original)
	before := snapshotTree(t, original)
	saved := stage.Root() + "-saved"
	if err := os.Rename(stage.Root(), saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(stage.Root()); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
		if err := os.Rename(saved, stage.Root()); err != nil {
			t.Error(err)
		}
	})
	if err := os.Symlink(original, stage.Root()); err != nil {
		t.Fatal(err)
	}
	result, err := Materialize(t.Context(), stage, "order.create/v1", interfacemeta.OperationKindCommand)
	if !errors.Is(err, ErrCreate) || !errors.Is(err, projectstage.ErrConcurrentChange) || result != (Result{}) {
		t.Fatalf("redirected stage: result=%#v, err=%v", result, err)
	}
	if !reflect.DeepEqual(snapshotTree(t, original), before) {
		t.Fatal("redirected stage mutated original source")
	}
}

func TestMaterializeRejectsUnsafeParents(t *testing.T) {
	for _, kind := range []string{"file", "external-link", "internal-link", "dangling-link", "target-link"} {
		t.Run(kind, func(t *testing.T) {
			original := materializeProject(t)
			stage := materializeStage(t, original)
			external := t.TempDir()
			writeRollbackFile(t, filepath.Join(external, "keep.txt"), "EXTERNAL\n")
			outsideBefore := snapshotTree(t, external)
			link := filepath.Join(stage.Root(), "interfaces")
			destination := external
			switch kind {
			case "file":
				writeRollbackFile(t, link, "AUTHORED_FILE\n")
			case "internal-link":
				destination = filepath.Join(stage.Root(), "other")
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			case "dangling-link":
				destination = filepath.Join(external, "missing")
			case "target-link":
				link = filepath.Join(link, "order", "create", "v1")
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "file" {
				if err := os.Symlink(destination, link); err != nil {
					t.Fatalf("create symbolic path: %v", err)
				}
			}
			before := snapshotTree(t, original)
			result, err := Materialize(t.Context(), stage, "order.create/v1", interfacemeta.OperationKindCommand)
			if !errors.Is(err, ErrCreate) || result != (Result{}) {
				t.Fatalf("unsafe parent: result=%#v, err=%v", result, err)
			}
			if kind == "target-link" {
				if !errors.Is(err, ErrTargetExists) {
					t.Fatalf("symbolic occupied target error = %v", err)
				}
			} else if kind != "file" && !errors.Is(err, atomicfs.ErrUnsafePath) {
				t.Fatalf("unsafe traversal error = %v", err)
			}
			if !reflect.DeepEqual(snapshotTree(t, external), outsideBefore) || !reflect.DeepEqual(snapshotTree(t, original), before) {
				t.Fatal("unsafe path rejection changed original or external source")
			}
			if kind == "internal-link" {
				if children, err := os.ReadDir(destination); err != nil || len(children) != 0 {
					t.Fatalf("followed internal symbolic parent: %v, %v", children, err)
				}
			}
		})
	}
}

func TestMaterializeRollsBackBothFilesAfterLateFailure(t *testing.T) {
	for _, failure := range []string{"cancellation", "original-drift"} {
		t.Run(failure, func(t *testing.T) {
			original := materializeProject(t)
			stage := materializeStage(t, original)
			before := snapshotTree(t, stage.Root())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observed := false
			checked := materializeCheckContext{Context: ctx, check: func() {
				if observed {
					return
				}
				directory := filepath.Join(stage.Root(), "interfaces", "order", "create", "v1")
				for _, name := range []string{"interface.go", "interface.yaml"} {
					if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
						return
					}
				}
				observed = true
				if failure == "cancellation" {
					cancel()
				} else {
					writeRollbackFile(t, filepath.Join(original, "user.txt"), "USER_EDIT\n")
				}
			}}
			result, err := Materialize(checked, stage, "order.create/v1", interfacemeta.OperationKindCommand)
			want := error(context.Canceled)
			if failure == "original-drift" {
				want = projectstage.ErrConcurrentChange
			}
			if !observed || !errors.Is(err, ErrCreate) || !errors.Is(err, want) || result != (Result{}) {
				t.Fatalf("late failure: observed=%t, result=%#v, err=%v", observed, result, err)
			}
			if !reflect.DeepEqual(snapshotTree(t, stage.Root()), before) {
				t.Fatal("failed materialization left a file, directory, or transaction residue")
			}
			if failure == "original-drift" {
				if data, err := os.ReadFile(filepath.Join(original, "user.txt")); err != nil || string(data) != "USER_EDIT\n" {
					t.Fatalf("lost concurrent source edit: %q, %v", data, err)
				}
			}
		})
	}
}

type materializeCheckContext struct {
	context.Context
	check func()
}

func (c materializeCheckContext) Err() error {
	c.check()
	return c.Context.Err()
}

func materializeProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeRollbackFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeRollbackFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	return root
}

func materializeStage(t *testing.T, original string) *projectstage.Stage {
	t.Helper()
	stage, err := projectstage.Create(t.Context(), projectstage.Options{
		Start:           original,
		TemporaryParent: t.TempDir(),
		Environment:     append(os.Environ(), "GOWORK=off", "GOFLAGS="),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close materialization stage: %v", err)
		}
	})
	return stage
}
