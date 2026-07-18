package capabilitycreate_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
)

func TestRenderImplementationWriteCreatesSafeUserOwnedProviderMethod(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.account\n")
	pluginSource := []byte("package account\n\ntype Plugin struct{}\n")
	if err := os.WriteFile(filepath.Join(root, "account", "plugin.go"), pluginSource, 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.go): %v", err)
	}
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.ModulePath() != "example.com/acme/app" {
		t.Fatalf("ModulePath = %q", plan.ModulePath())
	}
	write, changed, err := capabilitycreate.RenderImplementationWrite(plan)
	if err != nil || !changed {
		t.Fatalf("RenderImplementationWrite = changed %t, %#v, %v", changed, write, err)
	}
	if write.Path != "account/capability_account.register_v1.go" || !write.MustNotExist || write.Mode != 0o644 {
		t.Fatalf("implementation write = %#v", write)
	}
	for _, required := range [][]byte{
		[]byte("package account"),
		[]byte("contract \"example.com/acme/app/generated/go/contracts/account/register/v1\""),
		[]byte("func (*Plugin) Register(_ context.Context, _ contract.Request) (contract.Response, error)"),
		[]byte("kernelinvocation.ErrorUnavailable"),
		[]byte("\"implementation.unavailable\""),
	} {
		if !bytes.Contains(write.Data, required) {
			t.Fatalf("implementation source omits %q:\n%s", required, write.Data)
		}
	}
	if bytes.Contains(write.Data, []byte("TODO")) || bytes.Contains(write.Data, []byte("panic(")) {
		t.Fatalf("implementation source contains unsafe placeholder:\n%s", write.Data)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), write.Path, write.Data, parser.AllErrors); err != nil {
		t.Fatalf("parse implementation: %v\n%s", err, write.Data)
	}
	if got, err := os.ReadFile(filepath.Join(root, "account", "plugin.go")); err != nil || !bytes.Equal(got, pluginSource) {
		t.Fatalf("plugin.go changed = %q, %v", got, err)
	}
}

func TestRenderImplementationWritePreservesExistingPluginMethod(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.account\n")
	source := `package account

import (
	"context"
	contract "example.com/acme/app/generated/go/contracts/account/register/v1"
)

type Plugin struct{}

func (Plugin) Register(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`
	if err := os.WriteFile(filepath.Join(root, "account", "plugin.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.go): %v", err)
	}
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	write, changed, err := capabilitycreate.RenderImplementationWrite(plan)
	if err != nil || changed || write.Path != "" || write.Data != nil {
		t.Fatalf("existing implementation = changed %t, %#v, %v", changed, write, err)
	}
}

func TestRenderImplementationWriteRejectsUnsafePluginPackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources map[string]string
		want    string
	}{
		{name: "missing", sources: nil, want: "no non-test Go source"},
		{name: "main", sources: map[string]string{"plugin.go": "package main\n"}, want: "package main"},
		{name: "mixed", sources: map[string]string{"plugin.go": "package account\n", "other.go": "package other\n"}, want: "declare packages"},
		{name: "invalid", sources: map[string]string{"plugin.go": "package account\nfunc"}, want: "parse plugin.go"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := createModule(t)
			writePlugin(t, root, "account", "id: acme.account\n")
			for name, source := range test.sources {
				if err := os.WriteFile(filepath.Join(root, "account", name), []byte(source), 0o644); err != nil {
					t.Fatalf("WriteFile(%s): %v", name, err)
				}
			}
			plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			write, changed, err := capabilitycreate.RenderImplementationWrite(plan)
			if !errors.Is(err, capabilitycreate.ErrRenderImplementation) || !strings.Contains(err.Error(), test.want) || changed || write.Path != "" {
				t.Fatalf("RenderImplementationWrite = changed %t, %#v, %v; want %q", changed, write, err, test.want)
			}
		})
	}
	write, changed, err := capabilitycreate.RenderImplementationWrite(capabilitycreate.Plan{})
	if !errors.Is(err, capabilitycreate.ErrRenderImplementation) || changed || write.Path != "" {
		t.Fatalf("empty plan = changed %t, %#v, %v", changed, write, err)
	}
}
