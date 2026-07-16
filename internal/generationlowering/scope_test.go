package generationlowering_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/generationlowering"
)

func TestBuildScopeMergesAndCanonicallyOrdersRequests(t *testing.T) {
	t.Parallel()

	imports := []generationlowering.ImportRequest{
		{Path: "github.com/plystra/kernel/invocation", Name: "invocation", Source: "canonical dispatch"},
		{Path: "context", Name: "context", Source: "authn.verify/verify-session"},
		{Path: "example.com/app/generated/go/clients/authn/session/verify/v1", Name: "authnsessionverifyv1", Source: "authn.verify/verify-session"},
		{Path: "context", Name: "context", Source: "authz.check/authorize"},
		{Path: "context", Name: "context", Source: "authn.verify/verify-session"},
	}
	identifiers := []generationlowering.IdentifierRequest{
		{Name: "plystraAuthzCheckAllowed", Source: "authz.check/authorize response"},
		{Name: "plystraAuthnVerifyResponse", Source: "authn.verify/verify-session response"},
		{Name: "plystraAuthnVerifyError", Source: "authn.verify/verify-session error"},
		{Name: "plystraAuthnVerifyError", Source: "authn.verify/verify-session error"},
	}

	var first string
	permutations := 0
	forEachPermutation(imports, func(permutation []generationlowering.ImportRequest) {
		identifierPermutation := append([]generationlowering.IdentifierRequest(nil), identifiers...)
		if permutations%2 != 0 {
			slices.Reverse(identifierPermutation)
		}
		scope, err := generationlowering.BuildScope(permutation, identifierPermutation)
		if err != nil {
			t.Fatalf("BuildScope permutation %d: %v", permutations, err)
		}
		got := renderScope(scope)
		if first == "" {
			first = got
		} else if got != first {
			t.Fatalf("permutation %d changed scope:\n%s\nwant:\n%s", permutations, got, first)
		}
		permutations++
	})
	if permutations != 120 {
		t.Fatalf("permutations = %d, want 120", permutations)
	}
	want := strings.Join([]string{
		"import context context [authn.verify/verify-session, authz.check/authorize]",
		"import example.com/app/generated/go/clients/authn/session/verify/v1 authnsessionverifyv1 [authn.verify/verify-session]",
		"import github.com/plystra/kernel/invocation invocation [canonical dispatch]",
		"identifier plystraAuthnVerifyError authn.verify/verify-session error",
		"identifier plystraAuthnVerifyResponse authn.verify/verify-session response",
		"identifier plystraAuthzCheckAllowed authz.check/authorize response",
	}, "\n")
	if first != want {
		t.Fatalf("scope:\n%s\nwant:\n%s", first, want)
	}

	scope, err := generationlowering.BuildScope(imports, identifiers)
	if err != nil {
		t.Fatalf("BuildScope: %v", err)
	}
	gotImports := scope.Imports()
	gotIdentifiers := scope.Identifiers()
	gotImports[0] = generationlowering.Import{}
	gotIdentifiers[0] = generationlowering.Identifier{}
	sources := scope.Imports()[0].Sources()
	sources[0] = "changed"
	if renderScope(scope) != want {
		t.Fatal("Scope exposed mutable result storage")
	}
}

func TestBuildScopeRejectsCollisionsDeterministically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		imports     []generationlowering.ImportRequest
		identifiers []generationlowering.IdentifierRequest
		contains    []string
	}{
		{
			name: "different imports use one name",
			imports: []generationlowering.ImportRequest{
				{Path: "example.com/app/generated/go/clients/order/create/v1", Name: "ordercreatev1", Source: "orders.create/call"},
				{Path: "example.com/app/generated/go/clients/order/create-/v1", Name: "ordercreatev1", Source: "orders.compat/call"},
			},
			contains: []string{"identifier \"ordercreatev1\"", "clients/order/create/v1", "orders.create/call", "clients/order/create-/v1", "orders.compat/call"},
		},
		{
			name: "one import uses different names",
			imports: []generationlowering.ImportRequest{
				{Path: "example.com/app/generated/go/clients/order/create/v1", Name: "ordercreatev1", Source: "orders.create/call"},
				{Path: "example.com/app/generated/go/clients/order/create/v1", Name: "createclient", Source: "orders.audit/call"},
			},
			contains: []string{"import path", "createclient", "orders.audit/call", "ordercreatev1", "orders.create/call"},
		},
		{
			name: "import and local use one name",
			imports: []generationlowering.ImportRequest{
				{Path: "context", Name: "context", Source: "generated invocation signature"},
			},
			identifiers: []generationlowering.IdentifierRequest{
				{Name: "context", Source: "authn.verify/context-derivation"},
			},
			contains: []string{"identifier \"context\"", "import \"context\"", "generated invocation signature", "authn.verify/context-derivation"},
		},
		{
			name: "distinct locals use one name",
			identifiers: []generationlowering.IdentifierRequest{
				{Name: "plystraVerified", Source: "authn.verify/derive"},
				{Name: "plystraVerified", Source: "authz.check/derive"},
			},
			contains: []string{"identifier \"plystraVerified\"", "authn.verify/derive", "authz.check/derive"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var first string
			for iteration := 0; iteration < 2; iteration++ {
				imports := append([]generationlowering.ImportRequest(nil), test.imports...)
				identifiers := append([]generationlowering.IdentifierRequest(nil), test.identifiers...)
				if iteration != 0 {
					slices.Reverse(imports)
					slices.Reverse(identifiers)
				}
				_, err := generationlowering.BuildScope(imports, identifiers)
				if !errors.Is(err, generationlowering.ErrScope) || !errors.Is(err, generationlowering.ErrIdentifierCollision) {
					t.Fatalf("BuildScope error = %v, want ErrScope and ErrIdentifierCollision", err)
				}
				for _, want := range test.contains {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("BuildScope error %q omits %q", err, want)
					}
				}
				if first == "" {
					first = err.Error()
				} else if err.Error() != first {
					t.Fatalf("collision diagnostic changed: %q then %q", first, err)
				}
			}
		})
	}
}

func TestBuildScopeRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		imports     []generationlowering.ImportRequest
		identifiers []generationlowering.IdentifierRequest
		target      error
		want        string
	}{
		{name: "relative import", imports: []generationlowering.ImportRequest{{Path: "../private", Name: "private", Source: "node"}}, target: generationlowering.ErrInvalidImport, want: "path"},
		{name: "keyword import name", imports: []generationlowering.ImportRequest{{Path: "context", Name: "type", Source: "node"}}, target: generationlowering.ErrInvalidImport, want: "non-blank ASCII"},
		{name: "blank import name", imports: []generationlowering.ImportRequest{{Path: "context", Name: "_", Source: "node"}}, target: generationlowering.ErrInvalidImport, want: "non-blank ASCII"},
		{name: "unicode import name", imports: []generationlowering.ImportRequest{{Path: "context", Name: "cøntéxt", Source: "node"}}, target: generationlowering.ErrInvalidImport, want: "non-blank ASCII"},
		{name: "blank import source", imports: []generationlowering.ImportRequest{{Path: "context", Name: "context"}}, target: generationlowering.ErrInvalidImport, want: "source"},
		{name: "control in import source", imports: []generationlowering.ImportRequest{{Path: "context", Name: "context", Source: "node\nsecret"}}, target: generationlowering.ErrInvalidImport, want: "source"},
		{name: "invalid identifier", identifiers: []generationlowering.IdentifierRequest{{Name: "9value", Source: "node"}}, target: generationlowering.ErrInvalidIdentifier, want: "non-blank ASCII"},
		{name: "keyword identifier", identifiers: []generationlowering.IdentifierRequest{{Name: "var", Source: "node"}}, target: generationlowering.ErrInvalidIdentifier, want: "non-blank ASCII"},
		{name: "blank identifier source", identifiers: []generationlowering.IdentifierRequest{{Name: "value"}}, target: generationlowering.ErrInvalidIdentifier, want: "source"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := generationlowering.BuildScope(test.imports, test.identifiers)
			if !errors.Is(err, generationlowering.ErrScope) || !errors.Is(err, test.target) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildScope error = %v, want ErrScope, %v, and %q", err, test.target, test.want)
			}
		})
	}
}

func renderScope(scope generationlowering.Scope) string {
	lines := make([]string, 0, len(scope.Imports())+len(scope.Identifiers()))
	for _, imported := range scope.Imports() {
		lines = append(lines, fmt.Sprintf("import %s %s [%s]", imported.Path(), imported.Name(), strings.Join(imported.Sources(), ", ")))
	}
	for _, identifier := range scope.Identifiers() {
		lines = append(lines, fmt.Sprintf("identifier %s %s", identifier.Name(), identifier.Source()))
	}
	return strings.Join(lines, "\n")
}

func forEachPermutation[T any](values []T, visit func([]T)) {
	permutation := append([]T(nil), values...)
	var generate func(int)
	generate = func(index int) {
		if index == len(permutation) {
			visit(append([]T(nil), permutation...))
			return
		}
		for candidate := index; candidate < len(permutation); candidate++ {
			permutation[index], permutation[candidate] = permutation[candidate], permutation[index]
			generate(index + 1)
			permutation[index], permutation[candidate] = permutation[candidate], permutation[index]
		}
	}
	generate(0)
}
