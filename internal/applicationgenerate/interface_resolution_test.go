package applicationgenerate_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/projectcheck"
)

func TestGenerationAndCheckRejectInvalidConstructorGraphBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(testing.TB, string, []string) error
	}{
		{
			name: "generate",
			run: func(t testing.TB, root string, environment []string) error {
				_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment})
				return err
			},
		},
		{
			name: "generate check",
			run: func(t testing.TB, root string, environment []string) error {
				_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
				return err
			},
		},
		{
			name: "project check",
			run: func(t testing.TB, root string, environment []string) error {
				_, err := projectcheck.Check(t.Context(), projectcheck.Options{Start: root, Environment: environment})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeGenerationGraphFailureProject(t)
			before := snapshotTree(t, root)
			err := test.run(t, filepath.Join(root, "app"), mergedEnvironment(map[string]string{
				"GOFLAGS": "-mod=readonly",
				"GOWORK":  "off",
				"GOPROXY": "off",
				"GOSUMDB": "off",
			}))
			if !errors.Is(err, constructorgraph.ErrMissingBinding) || !strings.Contains(err.Error(), "audit.write/v1") || !strings.Contains(err.Error(), "before generation") {
				t.Fatalf("operation error = %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("operation mutated Project before rejecting graph:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func writeGenerationGraphFailureProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeModule(t, root, "example.com/generation-graph", "")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	writeFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeGenerationGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeGenerationGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/generation-graph/interfaces/app/run/v1"
	writev1 "example.com/generation-graph/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(audit writev1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	return root
}

func writeGenerationGraphInterface(t testing.TB, root, relative, packageName, identifier, method string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`, packageName, identifier, method))
}
