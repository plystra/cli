// Package constructorsymbol parses canonical fully qualified Go constructor
// symbols used to identify Plystra Implementations.
package constructorsymbol

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/mod/module"
)

// ErrInvalid reports a non-canonical fully qualified constructor symbol.
var ErrInvalid = errors.New("invalid constructor symbol")

// Symbol identifies one exported package-level constructor by its canonical Go
// import path and function name.
type Symbol struct {
	packagePath string
	function    string
}

// New validates and constructs a fully qualified constructor symbol.
func New(packagePath, function string) (Symbol, error) {
	if err := module.CheckImportPath(packagePath); err != nil {
		return Symbol{}, invalid(fmt.Sprintf("invalid Go import path %q: %v", packagePath, err))
	}
	if !token.IsIdentifier(function) || !ast.IsExported(function) {
		return Symbol{}, invalid(fmt.Sprintf("constructor name %q must be an exported Go identifier", function))
	}
	return Symbol{packagePath: packagePath, function: function}, nil
}

// Parse parses an exact symbol such as github.com/plystra/authz/rbac.New. The
// final dot separates the Go import path from the exported function name.
func Parse(value string) (Symbol, error) {
	separator := strings.LastIndexByte(value, '.')
	if separator <= 0 || separator == len(value)-1 {
		return Symbol{}, invalid("expected <go-import-path>.<exported-constructor>")
	}
	symbol, err := New(value[:separator], value[separator+1:])
	if err != nil {
		return Symbol{}, err
	}
	if symbol.String() != value {
		return Symbol{}, invalid("constructor symbol is not canonical")
	}
	return symbol, nil
}

// PackagePath returns the constructor package's canonical Go import path.
func (s Symbol) PackagePath() string { return s.packagePath }

// FunctionName returns the exported package-level constructor name.
func (s Symbol) FunctionName() string { return s.function }

// String returns the canonical fully qualified symbol, or an empty string for
// the zero value.
func (s Symbol) String() string {
	if s.packagePath == "" || s.function == "" {
		return ""
	}
	return s.packagePath + "." + s.function
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
