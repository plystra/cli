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
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/projectcheck"
)

func TestGenerationAndCheckCollectLocalApplicationRootsBeforeRejectingInvalidGraph(t *testing.T) {
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
			if !errors.Is(err, constructorgraph.ErrMissingBinding) || !strings.Contains(err.Error(), "audit.write/v1") || !strings.Contains(err.Error(), "//plystra:implements app.run/v1") || !strings.Contains(err.Error(), "before generation") {
				t.Fatalf("operation error = %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("operation mutated Project before rejecting graph:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestGenerationAndCheckRejectInvalidExposedInterfaceBeforeMutation(t *testing.T) {
	t.Parallel()

	operations := []struct {
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
	failures := []struct {
		name      string
		ambiguous bool
		wantError error
	}{
		{name: "missing Interface", wantError: interfaceresolution.ErrUnknownInterface},
		{name: "ambiguous Implementation", ambiguous: true, wantError: interfaceresolution.ErrAmbiguousImplementation},
	}
	for _, operation := range operations {
		operation := operation
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			for _, failure := range failures {
				failure := failure
				t.Run(failure.name, func(t *testing.T) {
					t.Parallel()
					root := writeExposedInterfaceFailureProject(t, failure.ambiguous)
					before := snapshotTree(t, root)
					err := operation.run(t, root, mergedEnvironment(map[string]string{
						"GOFLAGS": "-mod=readonly",
						"GOWORK":  "off",
						"GOPROXY": "off",
						"GOSUMDB": "off",
					}))
					if !errors.Is(err, failure.wantError) || !strings.Contains(err.Error(), "app.run/v1") {
						t.Fatalf("operation error = %v", err)
					}
					if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("operation mutated Project before rejecting exposed Interface:\nbefore: %#v\nafter:  %#v", before, after)
					}
				})
			}
		})
	}
}

func TestGenerationAndCheckRejectInvalidIntrinsicInterfaceBeforeMutation(t *testing.T) {
	t.Parallel()

	operations := []struct {
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
	failures := []struct {
		name   string
		shadow bool
		want   []string
	}{
		{name: "unknown reserved Interface", want: []string{"kernel.missing/v1", "selected Kernel API"}},
		{name: "application shadow", shadow: true, want: []string{"kernel.health/v1", "reserved kernel.* namespace"}},
	}
	for _, operation := range operations {
		operation := operation
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			for _, failure := range failures {
				failure := failure
				t.Run(failure.name, func(t *testing.T) {
					t.Parallel()
					root := writeIntrinsicInterfaceFailureProject(t, failure.shadow)
					before := snapshotTree(t, root)
					err := operation.run(t, root, mergedEnvironment(map[string]string{
						"GOFLAGS": "-mod=readonly",
						"GOWORK":  "off",
						"GOPROXY": "off",
						"GOSUMDB": "off",
					}))
					if !errors.Is(err, interfaceresolution.ErrResolve) || !containsAllInterfaceResolutionFragments(err.Error(), failure.want...) {
						t.Fatalf("operation error = %v", err)
					}
					if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("operation mutated Project before rejecting intrinsic Interface:\nbefore: %#v\nafter: %#v", before, after)
					}
				})
			}
		})
	}
}

func writeIntrinsicInterfaceFailureProject(t testing.TB, shadow bool) string {
	t.Helper()
	root := t.TempDir()
	writeModule(t, root, "example.com/intrinsic-interface", "")
	writeFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	if !shadow {
		writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.missing/v1]}\n")
		return root
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeGenerationGraphInterface(t, root, "kernel/health/v1", "healthv1", "kernel.health/v1", "Health")
	return root
}

func containsAllInterfaceResolutionFragments(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func writeExposedInterfaceFailureProject(t testing.TB, ambiguous bool) string {
	t.Helper()
	root := t.TempDir()
	writeModule(t, root, "example.com/exposed-interface", "")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "http: {expose: [app.run/v1]}\n")
	writeFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	if !ambiguous {
		return root
	}
	writeGenerationGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	for _, packageName := range []string{"appone", "apptwo"} {
		writeFile(t, filepath.Join(root, packageName, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	runv1 "example.com/exposed-interface/interfaces/app/run/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`, packageName))
	}
	return root
}

func writeGenerationGraphFailureProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeModule(t, root, "example.com/generation-graph", "")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
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
