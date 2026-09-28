package invocationgen_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/invocationgen"
	"github.com/plystra/cli/internal/testkernel"
)

func TestGeneratedErrorProjectionUsesBoundedKernelCarriers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("package boundary\nimport (\"context\"; kernelinvocation \"github.com/plystra/kernel/invocation\")\n")
	invocationgen.RenderTransportErrorInput(&source, []string{"rejected", "unavailable"})
	writeGeneratedFile(t, root, "boundary.go", []byte(source.String()))
	writeGeneratedFile(t, root, "boundary_test.go", []byte(boundedErrorProjectionTests))
	writeGeneratedFile(t, root, "go.mod", []byte(fmt.Sprintf("module example.com/boundary\n\ngo 1.26\nrequire github.com/plystra/kernel v0.0.0\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(testkernel.Root(t)))))
	command := exec.CommandContext(t.Context(), "go", "test", "-race", "-mod=mod", "./...", "-count=1")
	command.Dir = root
	command.Env = append(command.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated bounded error projection: %v\n%s", err, output)
	}
}

const boundedErrorProjectionTests = `package boundary_test
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	boundary "example.com/boundary"
	"github.com/plystra/kernel/invocation"
)
type cycle struct{}
func (e *cycle) Error() string { panic("private error text") }
func (e *cycle) Unwrap() error { return e }
type hostile struct { child error }
func (e hostile) Error() string { panic("private error text") }
func (e hostile) Is(error) bool { panic("custom Is called") }
func (e hostile) As(any) bool { panic("custom As called") }
func (e hostile) Unwrap() error { return e.child }
type panicUnwrap struct{}
func (panicUnwrap) Error() string { return "private" }
func (panicUnwrap) Unwrap() error { panic("private unwrap") }
func TestProjection(t *testing.T) {
	semantic := invocation.NewSemanticError("rejected", errors.New("private"))
	classified, err := invocation.NewError(invocation.ErrorUnavailable, "provider.unavailable")
	if err != nil { t.Fatal(err) }
	before, err := invocation.NewNotStartedError(invocation.ErrorInvalidArgument, "contract.request_invalid")
	if err != nil { t.Fatal(err) }
	for _, test := range []struct { name string; err error; code, class, completion string }{
		{"semantic", semantic, "rejected", "", "result_known"},
		{"wrapped", fmt.Errorf("private: %w", semantic), "rejected", "", "result_known"},
		{"joined same", errors.Join(semantic, semantic), "rejected", "", "result_known"},
		{"joined conflict", errors.Join(semantic, invocation.NewSemanticError("unavailable", nil)), "", "internal", "result_known"},
		{"runtime conflict", errors.Join(semantic, classified), "", "internal", "result_known"},
		{"unknown semantic", invocation.NewSemanticError("undeclared", nil), "", "internal", "result_known"},
		{"invalid semantic", invocation.NewSemanticError("PRIVATE", nil), "", "internal", "result_unknown"},
		{"uncertain semantic", invocation.NewSemanticError("rejected", invocation.NewResultUnknown(errors.New("private"))), "rejected", "", "result_unknown"},
		{"uncertain wrapped", fmt.Errorf("private: %w", invocation.NewResultUnknown(semantic)), "rejected", "", "result_unknown"},
		{"uncertain conflict", errors.Join(semantic, invocation.NewResultUnknown(classified)), "", "internal", "result_unknown"},
		{"runtime", classified, "", "unavailable", "result_known"},
		{"pre-entry", hostile{before}, "", "invalid_argument", "not_started"},
		{"custom hooks ignored", hostile{semantic}, "rejected", "", "result_known"},
		{"raw cancellation", context.Canceled, "", "cancelled", "result_unknown"},
		{"cycle", &cycle{}, "", "internal", "result_unknown"},
		{"unwrap panic", panicUnwrap{}, "", "internal", "result_unknown"},
		{"nil", nil, "", "internal", "result_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := boundary.SafeTransportError(test.err)
			if !got.Valid() || got.SemanticErrorCode() != test.code || got.KernelErrorClass() != test.class || got.Completion().String() != test.completion {
				t.Fatalf("projection = %#v", got)
			}
			if strings.Contains(fmt.Sprintf("%#v", got), "private") { t.Fatal("private value escaped") }
		})
	}
}
func TestTraversalLimits(t *testing.T) {
	var err error = invocation.NewSemanticError("rejected", nil)
	for depth := 0; depth <= 65; depth++ {
		got := boundary.SafeTransportError(err)
		if depth <= 64 && got.SemanticErrorCode() != "rejected" { t.Fatalf("rejected depth %d", depth) }
		if depth == 65 && (got.KernelErrorClass() != "internal" || got.Completion().String() != "result_unknown") { t.Fatal("depth bound missing") }
		err = hostile{err}
	}
	for _, count := range []int{1023, 1024} {
		children := make([]error, count)
		for i := range children { children[i] = invocation.NewSemanticError("rejected", nil) }
		got := boundary.SafeTransportError(errors.Join(children...))
		if count == 1023 && got.SemanticErrorCode() != "rejected" { t.Fatal("exact node limit rejected") }
		if count == 1024 && (got.KernelErrorClass() != "internal" || got.Completion().String() != "result_unknown") { t.Fatal("node bound missing") }
	}
}
`
