package constructorsymbol_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/constructorsymbol"
)

func TestParseCanonicalConstructorSymbols(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"github.com/plystra/authz/rbac.New",
		"github.com/acme/platform/v2/email/smtp.Build",
		"example.com/package.with.dots.NewService",
		"my-app.New",
		"my-app/internal/service.Établir",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			symbol, err := constructorsymbol.Parse(value)
			if err != nil {
				t.Fatalf("Parse(%q): %v", value, err)
			}
			if symbol.String() != value {
				t.Fatalf("String = %q, want %q", symbol.String(), value)
			}
			roundTrip, err := constructorsymbol.New(symbol.PackagePath(), symbol.FunctionName())
			if err != nil || roundTrip != symbol {
				t.Fatalf("New round trip = %#v, %v; want %#v", roundTrip, err, symbol)
			}
		})
	}
}

func TestParseRejectsNonCanonicalConstructorSymbols(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"New",
		".New",
		"github.com/acme/service.",
		"github.com/acme/service.new",
		"github.com/acme/service.func",
		"github.com/acme/service.New[T]",
		"github.com/acme/service.New More",
		"github.com/acme/service@v1.New",
		"github.com/acme\\service.New",
		"../service.New",
		"github.com/acme/service. New",
		"github.com/acme/service.New\x00",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if symbol, err := constructorsymbol.Parse(value); !errors.Is(err, constructorsymbol.ErrInvalid) || symbol.String() != "" {
				t.Fatalf("Parse(%q) = %#v, %v", value, symbol, err)
			}
		})
	}
}

func TestZeroSymbolIsEmpty(t *testing.T) {
	t.Parallel()

	var symbol constructorsymbol.Symbol
	if symbol.String() != "" || symbol.PackagePath() != "" || symbol.FunctionName() != "" {
		t.Fatalf("zero Symbol = %#v", symbol)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"github.com/plystra/authz/rbac.New",
		"my-app.New",
		"",
		"invalid",
		"../escape.New",
		"github.com/acme/service.new",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		symbol, err := constructorsymbol.Parse(value)
		if err != nil {
			if !errors.Is(err, constructorsymbol.ErrInvalid) {
				t.Fatalf("Parse(%q) error = %v", value, err)
			}
			return
		}
		if symbol.String() != value {
			t.Fatalf("Parse(%q).String() = %q", value, symbol.String())
		}
		roundTrip, err := constructorsymbol.New(symbol.PackagePath(), symbol.FunctionName())
		if err != nil || roundTrip != symbol {
			t.Fatalf("New round trip = %#v, %v; want %#v", roundTrip, err, symbol)
		}
	})
}
